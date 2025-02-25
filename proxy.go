package goproxy

import (
	"crypto/tls"
	"io"
	"net/http"
	"sync/atomic"
)

type CertStorage interface {
	Fetch(hostname string, gen func() (*tls.Certificate, error)) (*tls.Certificate, error)
}

// ProxyHttpServer is the proxy server implementation that implements http.Handler.
// You can directly add it to your net/http request router and use it as a normal HTTP handler.
type ProxyHttpServer struct {
	sess          atomic.Int64
	reqHandlers   []ReqHandler
	respHandlers  []RespHandler
	httpsHandlers []HttpsHandler
	opt           Options
}

var _ http.Handler = (*ProxyHttpServer)(nil)

func copyHeaders(dst, src http.Header, keepDestHeaders bool) {
	if !keepDestHeaders {
		for k := range dst {
			dst.Del(k)
		}
	}
	for k, vs := range src {
		// direct assignment to avoid canonicalization
		dst[k] = append([]string(nil), vs...)
	}
}

func (proxy *ProxyHttpServer) filterRequest(r *http.Request, ctx *ProxyCtx) (req *http.Request, resp *http.Response) {
	req = r
	for _, h := range proxy.reqHandlers {
		req, resp = h.Handle(req, ctx)
		// non-nil resp means the handler decided to skip sending the request
		// and return canned response instead.
		if resp != nil {
			break
		}
	}
	return
}

func (proxy *ProxyHttpServer) filterResponse(respOrig *http.Response, ctx *ProxyCtx) (resp *http.Response) {
	resp = respOrig
	for _, h := range proxy.respHandlers {
		ctx.Resp = resp
		resp = h.Handle(resp, ctx)
	}
	return
}

// RemoveProxyHeaders removes all proxy headers which should not propagate to the next hop.
func RemoveProxyHeaders(ctx *ProxyCtx, r *http.Request) {
	r.RequestURI = "" // this must be reset when serving a request with the client
	ctx.Options.Infof(ctx, "Sending request %v %v", r.Method, r.URL.String())
	if !ctx.Options.KeepAcceptEncoding {
		// If no Accept-Encoding header exists, Transport will add the headers it can accept
		// and would wrap the response body with the relevant reader.
		r.Header.Del("Accept-Encoding")
	}
	// curl can add that, see
	// https://jdebp.eu./FGA/web-proxy-connection-header.html
	r.Header.Del("Proxy-Connection")
	r.Header.Del("Proxy-Authenticate")
	r.Header.Del("Proxy-Authorization")
	// Connection, Authenticate and Authorization are single hop Header:
	// http://www.w3.org/Protocols/rfc2616/rfc2616.txt
	// 14.10 Connection
	//   The Connection general-header field allows the sender to specify
	//   options that are desired for that particular connection and MUST NOT
	//   be communicated by proxies over further connections.

	// We need to keep "Connection: upgrade" header, since it's part of
	// the WebSocket handshake, and it won't work without it.
	// For all the other cases (close, keep-alive), we already handle them, by
	// setting the r.Close variable in the previous lines.
	if !isWebSocketHandshake(r.Header) {
		r.Header.Del("Connection")
	}
}

type flushWriter struct {
	w io.Writer
}

func (fw flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if f, ok := fw.w.(http.Flusher); ok {
		// only flush if the Writer implements the Flusher interface.
		f.Flush()
	}

	return n, err
}

// Standard net/http function. Shouldn't be used directly, http.Serve will use it.
func (proxy *ProxyHttpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		proxy.handleHttps(w, r)
	} else {
		proxy.handleHttp(w, r)
	}
}

// NewProxyHttpServer creates and returns a proxy server, logging to stderr by default.
func NewProxyHttpServer(opt Options) *ProxyHttpServer {
	proxy := ProxyHttpServer{
		opt: opt,
	}
	return &proxy
}
