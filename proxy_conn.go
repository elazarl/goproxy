package goproxy

import (
	"io"
	"net"
	"net/http"
	"time"
)

type proxyConn struct {
	*net.TCPConn
	BytesWrote   int64
	BytesRead    int64
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// newProxyConn is a wrapper around a net.Conn that allows us to log the number of bytes
// written to the connection
func newProxyConn(conn *net.TCPConn) *proxyConn {
	c := &proxyConn{TCPConn: conn}
	return c
}

func (conn *proxyConn) Write(b []byte) (n int, err error) {
	conn.TCPConn.SetWriteDeadline(time.Now().Add(conn.WriteTimeout))
	n, err = conn.TCPConn.Write(b)
	if err != nil {
		return
	}
	conn.BytesWrote += int64(n)
	conn.TCPConn.SetWriteDeadline(time.Time{})
	return
}

func (conn *proxyConn) Read(b []byte) (n int, err error) {
	conn.TCPConn.SetReadDeadline(time.Now().Add(conn.ReadTimeout))
	n, err = conn.Read(b)
	if err != nil {
		return
	}
	conn.BytesRead += int64(n)
	conn.TCPConn.SetReadDeadline(time.Time{})
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
