package sniff

import (
	"errors"
	"io"
	"net"
	"time"
)

var (
	errFakeConn = errors.New("fake conn error")
)

type fakeConn struct {
	r io.Reader
}

func (s *fakeConn) Read(b []byte) (int, error) {
	return s.r.Read(b)
}

func (s *fakeConn) Write(b []byte) (int, error) {
	return 0, errFakeConn
}

func (s *fakeConn) Close() error {
	return errFakeConn
}

func (s *fakeConn) LocalAddr() net.Addr {
	return nil
}

func (s *fakeConn) RemoteAddr() net.Addr {
	return nil
}

func (s *fakeConn) SetDeadline(t time.Time) error {
	return errFakeConn
}

func (s *fakeConn) SetReadDeadline(t time.Time) error {
	return errFakeConn
}

func (s *fakeConn) SetWriteDeadline(t time.Time) error {
	return errFakeConn
}
