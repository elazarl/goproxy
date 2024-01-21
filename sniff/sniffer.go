package sniff

import (
	"bytes"
	"net"
	"sync"
	"time"
)

func NewSniffer(conn net.Conn) *Sniffer {
	s := &Sniffer{conn: conn}
	s.mu.Lock()
	return s
}

type Sniffer struct {
	conn net.Conn

	clientHelloRecord bytes.Buffer
	mu                sync.Mutex

	clientHelloRecordSize int
}

func (s *Sniffer) Read(b []byte) (int, error) {
	bytesRead, err := s.conn.Read(b)
	if err != nil {
		return bytesRead, err
	}

	if s.clientHelloRecordSize == 0 {
		length := int(b[3])<<8 | int(b[4])   // data length
		s.clientHelloRecordSize = length + 5 // with record header
	}

	left := s.clientHelloRecordSize - s.clientHelloRecord.Len()
	if left == 0 {
		return bytesRead, nil
	}

	bytesToSniff := left
	if bytesToSniff > bytesRead {
		bytesToSniff = bytesRead
	}

	s.clientHelloRecord.Write(b[:bytesToSniff])

	if left == bytesRead {
		s.mu.Unlock()
	}

	return bytesRead, nil
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

	return s.clientHelloRecord.Bytes(), nil
}
