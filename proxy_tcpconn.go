package goproxy

import (
	"fmt"
	"net"
	"reflect"
	"syscall"
	"time"

	"github.com/function61/gokit/logex"
)

type proxyTCPConn struct {
	Conn         net.TCPConn
	BytesWrote   int64
	BytesRead    int64
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	logger       *logex.Leveled
}

type dumbResponseWriter struct {
	net.Conn
}

// newProxyTCPConn is a wrapper around a net.TCPConn that allows us to log the number of bytes
// written to the connection
func newProxyTCPConn(conn interface{}) (*proxyTCPConn, error) {
	var tcpConn *net.TCPConn
	switch conn.(type) {
	case *net.TCPConn:
		tcpConn = conn.(*net.TCPConn)
	case dumbResponseWriter:
		v, ok := conn.(*net.TCPConn)
		if ok {
			tcpConn = v
		} else {
			return nil, fmt.Errorf("Unable to convert to TCPConn from %+v", reflect.TypeOf(conn))
		}
	default:
		return nil, fmt.Errorf("Unable to convert to TCPConn from %+v", reflect.TypeOf(conn))
	}
	c := &proxyTCPConn{Conn: *tcpConn}
	return c, nil
}

func (conn *proxyTCPConn) Write(b []byte) (n int, err error) {
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

func (conn *proxyTCPConn) Read(b []byte) (n int, err error) {
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

func (conn *proxyTCPConn) setKeepaliveParameters(count, interval, period int) error {
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
