package goproxy

import (
	"net/http"
	"net"
	"bufio"
	"errors"
)

// This response writer can be hijacked multiple times
type hijackedResponseWriter struct {
	nested http.ResponseWriter
	conn   net.Conn
	err    error
}

func (writer *hijackedResponseWriter) Header() http.Header {
	return writer.nested.Header()
}

func (writer *hijackedResponseWriter) Write(data []byte) (int, error) {
	return writer.nested.Write(data)
}

func (writer *hijackedResponseWriter) WriteHeader(code int) {
	writer.nested.WriteHeader(code)
}

func (writer *hijackedResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := writer.nested.(http.Hijacker)

	if !ok {
		return nil, nil, errors.New("proxy: nested http.ResponseWriter does not implement http.Hijacker interface")
	}

	if !writer.hijacked() {
		writer.conn, _, writer.err = hijacker.Hijack()
	}

	return writer.conn, nil, writer.err
}

func (writer *hijackedResponseWriter) hijacked() bool {
	return writer.conn != nil || writer.err != nil
}
