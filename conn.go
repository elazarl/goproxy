package goproxy

import (
	"bufio"
	"errors"
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
	w.WriteHeader(http.StatusOK)
	n, e = w.dst.Write(data)
	return
}

func (w *connResponseWriter) WriteHeader(code int) {
	if w.header_written {
		return
	}

	w.header_written = true
	w.header.Write(w.dst)
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
