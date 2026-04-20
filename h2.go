package goproxy

import (
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/http2"
)

// h2Conn adapts an io.Reader/io.Writer pair into a net.Conn for
// http2.Server.ServeConn.
type h2Conn struct {
	r    io.Reader
	w    io.Writer
	conn net.Conn
}

func (c *h2Conn) Read(p []byte) (int, error)        { return c.r.Read(p) }
func (c *h2Conn) Write(p []byte) (int, error)       { return c.w.Write(p) }
func (c *h2Conn) Close() error                      { return c.conn.Close() }
func (c *h2Conn) LocalAddr() net.Addr               { return c.conn.LocalAddr() }
func (c *h2Conn) RemoteAddr() net.Addr              { return c.conn.RemoteAddr() }
func (c *h2Conn) SetDeadline(t time.Time) error     { return c.conn.SetDeadline(t) }
func (c *h2Conn) SetReadDeadline(t time.Time) error { return c.conn.SetReadDeadline(t) }
func (c *h2Conn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

// serveH2 terminates an HTTP/2 client session on the already-decrypted
// conn, decoding each stream into a standard *http.Request and handing it
// to the proxy's normal HTTP handler (filterRequest → RoundTrip →
// filterResponse). The caller must have already consumed the HTTP/2
// client preface ("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n").
//
// remoteAddr is the original client address (typically from the outer
// CONNECT request) and is propagated to each decoded *http.Request so
// downstream handlers see the real client rather than an intermediate.
//
// parentCtx, if non-nil, supplies UserData and RoundTripper that should
// be inherited by every per-stream ProxyCtx — matching the behaviour of
// the HTTP/1.1 path in handleHttps.
func (proxy *ProxyHttpServer) serveH2(
	clientReader io.Reader,
	clientConn net.Conn,
	host, remoteAddr string,
	parentCtx *ProxyCtx,
) {
	// Prepend the client preface so http2.Server.ServeConn can read it.
	preface := io.MultiReader(strings.NewReader(http2.ClientPreface), clientReader)
	conn := &h2Conn{r: preface, w: clientConn, conn: clientConn}

	h2s := &http2.Server{}
	h2s.ServeConn(conn, &http2.ServeConnOpts{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			// Build an absolute URL so handleHttp treats it as a proxy request.
			if req.URL.Host == "" {
				req.URL.Host = host
			}
			if req.URL.Scheme == "" {
				req.URL.Scheme = "https"
			}
			// Drop the default port for the scheme so the URL serialization
			// matches the HTTP/1.1 MITM path (which uses the CONNECT
			// request's Host header, typically without :443/:80). This
			// keeps URL-based logging and routing consistent across h1/h2.
			if h, p, ok := strings.Cut(req.URL.Host, ":"); ok {
				if (req.URL.Scheme == "https" && p == "443") ||
					(req.URL.Scheme == "http" && p == "80") {
					req.URL.Host = h
				}
			}
			req.RequestURI = ""
			if remoteAddr != "" {
				req.RemoteAddr = remoteAddr
			}

			proxy.handleHttpWithParent(w, req, parentCtx)
		}),
	})
}
