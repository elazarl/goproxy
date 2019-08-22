package goproxy

import (
	"io"
	"net"
	"net/http"
)

type proxyConn struct {
	net.Conn
	BytesWrote int64
}

// newProxyConn is a wrapper around a net.Conn that allows us to log the number of bytes
// written to the connection
func newProxyConn(conn net.Conn) *proxyConn {
	c := &proxyConn{Conn: conn}
	return c
}

func (conn *proxyConn) Write(b []byte) (n int, err error) {
	n, err = conn.Conn.Write(b)
	if err != nil {
		return
	}
	conn.BytesWrote += int64(n)

	return
}

type responseAndError struct {
	resp *http.Response
	err  error
}

// connCloser implements a wrapper containing an io.ReadCloser and a net.Conn
type connCloser struct {
	io.ReadCloser
	Conn net.Conn
}

// Close closes the connection and the io.ReadCloser
func (cc connCloser) Close() error {
	cc.Conn.Close()
	return cc.ReadCloser.Close()
}
