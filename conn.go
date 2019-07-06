package goproxy

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"strings"
)

type connResponseWriter struct {
	dst            io.Writer
	header         http.Header
	header_written bool
}

func (w *connResponseWriter) Header() http.Header {
	return w.header
}

func (w *connResponseWriter) Write(data []byte) (n int, e error) {
	if !w.header_written {
		w.WriteHeader(http.StatusOK)
	}
	return w.dst.Write(data)
}

func (w *connResponseWriter) WriteHeader(code int) {
	if w.header_written {
		return
	}

	_, err := io.WriteString(w.dst, fmt.Sprintf("HTTP/1.1 %d %s\r\n", code, http.StatusText(code)))
	if err != nil {
		return
	}
	w.header.Write(w.dst)
	w.header_written = true
}

func (w *connResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, ok := w.dst.(net.Conn)

	if !ok {
		return nil, nil, errors.New("proxy: nested io.Writer does not implement net.Conn interface")
	}

	rw := bufio.NewReadWriter(
		bufio.NewReader(io.MultiReader()),
		bufio.NewWriter(ioutil.Discard),
	)

	return conn, rw, nil
}

func NewConnResponseWriter(dst io.Writer) *connResponseWriter {
	return &connResponseWriter{
		dst:    dst,
		header: map[string][]string{},
	}
}

func Error(out http.ResponseWriter, err error, code int) {
	resp := &http.Response{
		StatusCode:    code,
		ContentLength: -1,
		Body:          ioutil.NopCloser(strings.NewReader(err.Error())),
	}

	ctx := &ProxyCtx{
		Req:       nil,
		Session:   0,
		Websocket: false,
		proxy:     nil,
	}

	writeResponse(ctx, resp, out)
}
