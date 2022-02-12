package goproxy

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"syscall"
	"time"

	tproxy "github.com/Windscribe/go-tproxy"
	"github.com/Windscribe/go-vhost"
	"golang.org/x/sys/unix"
)

type ProxyTCPConn struct {
	net.Conn
	BytesWrote           int64
	BytesRead            int64
	ReadTimeout          time.Duration
	WriteTimeout         time.Duration
	Logger               *ProxyLeveledLogger
	IgnoreDeadlineErrors bool
}

// newProxyTCPConn is a wrapper around a net.TCPConn that allows us to log the number of bytes
// written to the connection
func newProxyTCPConn(conn net.Conn) *ProxyTCPConn {
	return &ProxyTCPConn{Conn: conn}
}

func (conn *ProxyTCPConn) Close() error {
	if conn == nil || conn.Conn == nil {
		return nil
	}
	return conn.Conn.Close()
}

func (conn *ProxyTCPConn) Write(b []byte) (n int, err error) {
	if conn == nil || conn.Conn == nil {
		return 0, io.ErrUnexpectedEOF
	}
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

func (conn *ProxyTCPConn) Read(b []byte) (n int, err error) {
	if conn == nil || conn.Conn == nil {
		return 0, io.ErrUnexpectedEOF
	}
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

func (conn *ProxyTCPConn) SetKeepaliveParameters(sharedConn bool, count, interval, period int) error {
	var tcpConn *net.TCPConn
	var converted bool
	if sharedConn {

		if nConn, ok := conn.Conn.(*vhost.TLSConn); ok {
			tcpConn, ok = nConn.SharedConn.Conn.(*net.TCPConn)
			if ok {
				converted = true
			}
		} else if nConn, ok := conn.Conn.(*tproxy.Conn); ok {
			tcpConn = nConn.TCPConn
			converted = true
		} else {
			return fmt.Errorf("unable to set keep alives, conn is unkown type: %v", reflect.TypeOf(conn.Conn))
		}

	} else {
		tcpConn, converted = conn.Conn.(*net.TCPConn)
	}
	if !converted {
		return fmt.Errorf("Could not convert proxy conn from %v to net.TCPConn", reflect.TypeOf(conn.Conn))
	}
	setErr := tcpConn.SetKeepAlive(true)
	if setErr != nil {
		return setErr
	}
	setErr = tcpConn.SetKeepAlivePeriod(time.Duration(period) * time.Second)
	if setErr != nil {
		return setErr
	}

	setErr = tcpConn.SetLinger(0)
	if setErr != nil {
		return setErr
	}

	rawConn, err := tcpConn.SyscallConn()
	if err != nil {
		return err
	}

	tcpUserTimeout := ((period + interval*count) - 1) * 1000

	err = rawConn.Control(
		func(fdPtr uintptr) {
			// got socket file descriptor. Setting parameters.
			fd := int(fdPtr)
			//Number of probes.
			err := syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, syscall.TCP_KEEPCNT, count)
			if err != nil {
				conn.Logger.Warningf("on setting keepalive probe count: %s", err.Error())
			}
			//Wait time after an unsuccessful probe.
			err = syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, syscall.TCP_KEEPINTVL, interval)
			if err != nil {
				conn.Logger.Warningf("on setting keepalive retry interval: %s", err.Error())
			}
			//Set the user timeout to make sure connections close
			err = syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, unix.TCP_USER_TIMEOUT, int(tcpUserTimeout))
			if err != nil {
				conn.Logger.Warningf("on setting user timeout to %v: %s", tcpUserTimeout, err.Error())
			}
		})
	if err != nil {
		return err
	}
	return nil
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
