package goproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/net/http2"
)

// h2StreamConn wraps an HTTP/2 CONNECT stream as a net.Conn.
//
// When a CONNECT request arrives over HTTP/2, hijacking is unavailable.
// We implement net.Conn directly on top of the H2 stream:
//   - Read: r.Body  (the request body carries client -> proxy data)
//   - Write: w      (the response body carries proxy -> client data)
type h2StreamConn struct {
	r      io.ReadCloser
	w      http.ResponseWriter
	ctrl   *http.ResponseController
	local  net.Addr
	remote net.Addr
}

// responseWriterProvider is implemented by h2StreamConn so that httpError
// can recover an http.ResponseWriter from an io.Writer in H2 mode.
type responseWriterProvider interface {
	ResponseWriter() http.ResponseWriter
}

func newH2StreamConn(w http.ResponseWriter, r *http.Request) *h2StreamConn {
	return &h2StreamConn{
		r:      r.Body,
		w:      w,
		ctrl:   http.NewResponseController(w),
		local:  h2streamAddr("h2-proxy"),
		remote: h2streamAddr(r.RemoteAddr),
	}
}

func (c *h2StreamConn) ResponseWriter() http.ResponseWriter {
	return c.w
}

func (c *h2StreamConn) Read(b []byte) (int, error) {
	return c.r.Read(b)
}

func (c *h2StreamConn) Write(b []byte) (int, error) {
	n, err := c.w.Write(b)
	if err == nil {
		_ = c.ctrl.Flush()
	}
	return n, err
}

func (c *h2StreamConn) Close() error {
	return c.r.Close()
}

func (c *h2StreamConn) LocalAddr() net.Addr {
	return c.local
}

func (c *h2StreamConn) RemoteAddr() net.Addr {
	return c.remote
}

func (c *h2StreamConn) SetDeadline(t time.Time) error {
	rerr := c.ctrl.SetReadDeadline(t)
	werr := c.ctrl.SetWriteDeadline(t)
	if rerr != nil {
		return rerr
	}
	return werr
}

func (c *h2StreamConn) SetReadDeadline(t time.Time) error {
	return c.ctrl.SetReadDeadline(t)
}

func (c *h2StreamConn) SetWriteDeadline(t time.Time) error {
	return c.ctrl.SetWriteDeadline(t)
}

type h2streamAddr string

func (a h2streamAddr) Network() string {
	return "h2"
}

func (a h2streamAddr) String() string {
	return string(a)
}

// serveH2Mitm serves an HTTP/2 MITM connection via an embedded http2.Server.
//   - client is the underlying connection (*tls.Conn for ALPN-h2, plain net.Conn for h2c).
//   - host is the CONNECT target (e.g. "example.com:443").
//   - parentCtx carries the UserData / CertStore / RoundTripper from the CONNECT handler,
//     propagated into every per-stream ProxyCtx.
func (proxy *ProxyHttpServer) serveH2Mitm(client net.Conn, host string, parentCtx *ProxyCtx) {
	scheme := "https"
	if _, isTLS := client.(*tls.Conn); !isTLS {
		scheme = "http"
	}

	proxy.h2Server.ServeConn(client, &http2.ServeConnOpts{
		Context: context.Background(),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			proxy.handleH2MitmStream(w, r, host, scheme, parentCtx)
		}),
	})
}

// handleH2MitmStream handles a single HTTP/2 stream inside a MITM session,
// running request/response filters and forwarding to the upstream server.
func (proxy *ProxyHttpServer) handleH2MitmStream(
	w http.ResponseWriter,
	r *http.Request,
	host, scheme string,
	parentCtx *ProxyCtx,
) {
	// r.Host contains the :authority pseudo-header: prefer it over the
	// fallback CONNECT host so virtual-hosting works correctly.
	if r.Host != "" {
		r.URL.Host = r.Host
	} else if r.URL.Host == "" {
		r.URL.Host = host
	}
	r.URL.Scheme = scheme

	// Carry over the connecting client's address so that IP-matching
	// filters (e.g. SrcIpIs) keep working.
	r.RemoteAddr = parentCtx.Req.RemoteAddr

	reqCtx, finishRequest := context.WithCancel(r.Context())
	defer finishRequest()
	r = r.WithContext(reqCtx)

	ctx := &ProxyCtx{
		Req:          r,
		Session:      atomic.AddInt64(&proxy.sess, 1),
		Proxy:        proxy,
		UserData:     parentCtx.UserData,
		RoundTripper: parentCtx.RoundTripper,
		certStore:    parentCtx.certStore,
	}

	req, resp := proxy.filterRequest(r, ctx)
	if resp == nil {
		removeH2HopByHopHeaders(req)
		if !proxy.KeepHeader {
			RemoveProxyHeaders(ctx, req)
		}

		// bodyless h2 requests arrive with a non-nil empty Body; forwarding as-is
		// makes the upstream transport send a phantom body (-1). keep it bodyless.
		if req.ContentLength == 0 {
			req.Body = http.NoBody
		}

		var err error
		resp, err = ctx.RoundTrip(req)
		if err != nil {
			ctx.Warnf("HTTP/2 MITM: upstream RoundTrip failed: %v", err)
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		ctx.Logf("resp %v", resp.Status)
	}

	origBody := resp.Body
	resp = proxy.filterResponse(resp, ctx)
	defer resp.Body.Close()

	copyHeaders(w.Header(), resp.Header, proxy.KeepDestinationHeaders)

	// If the body was replaced by a filter, drop Content-Length so
	// the http2 framer can stream it without a length mismatch.
	if resp.Body != origBody {
		w.Header().Del("Content-Length")
	}

	// Announce pre-known trailers before WriteHeader (see handleHttp in http.go).
	announcedTrailers := len(resp.Trailer)
	if announcedTrailers > 0 {
		trailerKeys := make([]string, 0, announcedTrailers)
		for k := range resp.Trailer {
			trailerKeys = append(trailerKeys, k)
		}
		w.Header().Add("Trailer", strings.Join(trailerKeys, ", "))
	}

	w.WriteHeader(resp.StatusCode)

	if resp.Body != nil {
		if shouldFlushStreaming(resp) {
			// Streaming (gRPC/Connect/SSE/chunked): flush each chunk so it reaches the
			// client as it arrives. Mirrors net/http/httputil.ReverseProxy.flushInterval.
			rc := http.NewResponseController(w)
			buf := make([]byte, 32*1024)
			for {
				nr, er := resp.Body.Read(buf)
				if nr > 0 {
					if _, ew := w.Write(buf[:nr]); ew != nil {
						ctx.Warnf("HTTP/2 MITM: error writing response body: %v", ew)
						break
					}
					_ = rc.Flush()
				}
				if er != nil {
					// Mirror net/http/httputil.ReverseProxy.copyBuffer: io.EOF is the
					// normal end of stream and context.Canceled means the client went
					// away or cancelled the request, so neither is worth logging.
					if er != io.EOF && !errors.Is(er, context.Canceled) {
						ctx.Warnf("HTTP/2 MITM: error reading response body: %v", er)
					}
					break
				}
			}
		} else {
			// Fixed-length response: let the h2 server batch writes for throughput.
			if _, err := io.Copy(w, resp.Body); err != nil {
				ctx.Warnf("HTTP/2 MITM: error writing response body: %v", err)
			}
		}
	}

	// Forward response trailers after the body: pre-announced by name, the rest
	// (h2/gRPC send them unannounced) via http.TrailerPrefix; Flush forces
	// chunking. Mirrors handleHttp in http.go.
	if len(resp.Trailer) > 0 {
		if rc := http.NewResponseController(w); rc != nil {
			_ = rc.Flush()
		}
		if len(resp.Trailer) == announcedTrailers {
			copyHeaders(w.Header(), resp.Trailer, proxy.KeepDestinationHeaders)
		} else {
			for k, vs := range resp.Trailer {
				k = http.TrailerPrefix + k
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
		}
	}
}

// shouldFlushStreaming reports whether resp should be forwarded with a flush after
// each chunk, mirroring net/http/httputil.ReverseProxy.flushInterval: Server-Sent
// Events, or any response with an unknown (-1) Content-Length (gRPC, Connect, chunked).
func shouldFlushStreaming(resp *http.Response) bool {
	if baseCT, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); baseCT == "text/event-stream" {
		return true
	}
	return resp.ContentLength == -1
}

// removeH2HopByHopHeaders deletes HTTP/1.x hop-by-hop headers that are
// illegal in HTTP/2 (RFC 9113 §8.2.2).
func removeH2HopByHopHeaders(r *http.Request) {
	r.Header.Del("Connection")
	r.Header.Del("Keep-Alive")
	r.Header.Del("Proxy-Connection")
	r.Header.Del("Transfer-Encoding")
	r.Header.Del("Upgrade")
}
