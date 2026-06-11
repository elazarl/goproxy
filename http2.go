package goproxy

import (
	"io"
	"net"
	"net/http"
	"time"
)

// h2StreamConn wraps an HTTP/2 CONNECT stream as a net.Conn.
//
// This is made because when a request is served over HTTP/2, hijacking is not available.
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

type ResponseWriterProvider interface {
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
//   - client is the underlying connection (e.g. *tls.Conn for ALPN-h2, or plain net.Conn for h2c).
//   - host is the CONNECT target (e.g. "example.com:443").
//   - parentCtx carries the UserData / CertStore / RoundTripper from the CONNECT handler,
//     so they can be propagated into every per-stream ProxyCtx.
func (proxy *ProxyHttpServer) serveH2Mitm(client net.Conn, host string, parentCtx *ProxyCtx) {
	// TODO
}
