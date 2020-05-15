package goproxy

import (
	"io"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/function61/gokit/logex"
)

type proxyConn struct {
	Conn         net.TCPConn
	BytesWrote   int64
	BytesRead    int64
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	logger       *logex.Leveled
}

// newProxyConn is a wrapper around a net.Conn that allows us to log the number of bytes
// written to the connection
func newProxyConn(conn net.Conn) *proxyConn {
	tcpConn := conn.(*net.TCPConn)
	c := &proxyConn{Conn: *tcpConn}
	return c
}

func (conn *proxyConn) Write(b []byte) (n int, err error) {
	if conn.WriteTimeout > 0 {
		conn.Conn.SetWriteDeadline(time.Now().Add(conn.WriteTimeout))
	}
	n, err = conn.Conn.Write(b)
	if err != nil {
		return
	}
	conn.BytesWrote += int64(n)
	conn.Conn.SetWriteDeadline(time.Time{})
	return
}

func (conn *proxyConn) Read(b []byte) (n int, err error) {
	if conn.ReadTimeout > 0 {
		conn.Conn.SetReadDeadline(time.Now().Add(conn.ReadTimeout))
	}
	n, err = conn.Conn.Read(b)
	if err != nil {
		return
	}
	conn.BytesRead += int64(n)
	conn.Conn.SetReadDeadline(time.Time{})
	return
}

type responseAndError struct {
	resp *http.Response
	err  error
}

// connCloser implements a wrapper containing an io.ReadCloser and a net.Conn
type connCloser struct {
	io.ReadCloser
	Conn net.TCPConn
}

// Close closes the connection and the io.ReadCloser
func (cc connCloser) Close() error {
	cc.Conn.Close()
	return cc.ReadCloser.Close()
}

func (conn *proxyConn) setKeepaliveParameters(count, interval, period int) error {
	conn.Conn.SetKeepAlive(true)
	conn.Conn.SetKeepAlivePeriod(time.Duration(period) * time.Second)
	rawConn, err := conn.Conn.SyscallConn()
	if err != nil {
		return err
	}
	rawConn.Control(
		func(fdPtr uintptr) {
			// got socket file descriptor. Setting parameters.
			fd := int(fdPtr)
			//Number of probes.
			err := syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, syscall.TCP_KEEPCNT, count)
			if err != nil {
				conn.logger.Error.Printf("on setting keepalive probe count: %s", err.Error())
			}
			//Wait time after an unsuccessful probe.
			err = syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, syscall.TCP_KEEPINTVL, interval)
			if err != nil {
				conn.logger.Error.Printf("on setting keepalive retry interval: %s", err.Error())
			}
		})
	return nil
}
