package sniff

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"net"
	"reflect"
	"sync"
	"time"
)

func NewSniffer(conn net.Conn) *Sniffer {
	return &Sniffer{conn: conn, buf: new(bytes.Buffer)}
}

type Sniffer struct {
	conn        net.Conn
	buf         *bytes.Buffer
	clientHello []byte
	mu          sync.Mutex
}

func (s *Sniffer) Read(b []byte) (int, error) {
	n, err := s.conn.Read(b)
	if err != nil {
		return n, err
	}

	if s.buf == nil {
		// already read client hello
		return n, nil
	}

	go func() {
		buf := s.buf
		if buf == nil {
			return
		}
		buf.Write(b[:n])
	}()

	return n, nil
}

func (s *Sniffer) Write(b []byte) (int, error) {
	return s.conn.Write(b)
}

func (s *Sniffer) Close() error {
	return s.conn.Close()
}

func (s *Sniffer) LocalAddr() net.Addr {
	return s.conn.LocalAddr()
}

func (s *Sniffer) RemoteAddr() net.Addr {
	return s.conn.RemoteAddr()
}

func (s *Sniffer) SetDeadline(t time.Time) error {
	return s.conn.SetDeadline(t)
}

func (s *Sniffer) SetReadDeadline(t time.Time) error {
	return s.conn.SetReadDeadline(t)
}

func (s *Sniffer) SetWriteDeadline(t time.Time) error {
	return s.conn.SetWriteDeadline(t)
}

func (s *Sniffer) ReadClientHello() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.clientHello != nil {
		return s.clientHello, nil
	}

	conn := &fakeConn{r: s.buf}

	server := tls.Server(conn, nil)
	defer server.Close()

	msg, err := readHandshake(server, nil)
	if err != nil {
		return nil, err
	}

	v := reflect.ValueOf(msg)
	if t := v.Type().String(); t != "*tls.clientHelloMsg" {
		return nil, fmt.Errorf("sniffer: unexpected type: %s", t)
	}

	clientHelloRecord := []byte{0x16, 0x03, 0x01, 0x02, 0x00}
	clientHelloRecord = append(clientHelloRecord, v.Elem().FieldByName("raw").Bytes()...)

	s.clientHello = clientHelloRecord
	s.buf = nil

	return clientHelloRecord, nil
}
