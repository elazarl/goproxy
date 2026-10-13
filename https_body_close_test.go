package goproxy_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elazarl/goproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type trackedResponseBody struct {
	io.Reader
	closes int
	err    error
}

func (b *trackedResponseBody) Close() error {
	b.closes++
	return b.err
}

type responseWriteFailureConn struct {
	net.Conn
	writes int
	failAt int
	err    error
}

func (c *responseWriteFailureConn) Write(p []byte) (int, error) {
	c.writes++
	if c.writes == c.failAt {
		return 0, c.err
	}
	return c.Conn.Write(p)
}

type responseCloseRecorder struct {
	*httptest.ResponseRecorder
	conn net.Conn
}

func (w *responseCloseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn)), nil
}

func TestMITMResponseBodyClosedOnce(t *testing.T) {
	writeErr := errors.New("test response write failure")
	closeErr := errors.New("test response close failure")
	for _, tc := range []struct {
		name     string
		failAt   int
		closeErr error
		wantErr  error
	}{
		{name: "success"},
		{name: "header write failure", failAt: 2, wantErr: writeErr},
		{name: "body write failure", failAt: 3, wantErr: writeErr},
		{name: "close failure", closeErr: closeErr, wantErr: closeErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
			require.NoError(t, client.SetDeadline(time.Now().Add(5*time.Second)))
			conn := &responseWriteFailureConn{Conn: server, failAt: tc.failAt, err: writeErr}
			body := &trackedResponseBody{Reader: strings.NewReader("ok"), err: tc.closeErr}
			contexts := make(chan context.Context, 1)
			var logs bytes.Buffer
			proxy := goproxy.NewProxyHttpServer()
			proxy.Logger = log.New(&logs, "", 0)
			proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
			proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
				contexts <- ctx.Req.Context()
				resp := goproxy.NewResponse(req, goproxy.ContentTypeText, http.StatusOK, "ok")
				resp.Body = body
				return req, resp
			})
			req := httptest.NewRequestWithContext(t.Context(), http.MethodConnect, "http://example.test:80", nil)
			go proxy.ServeHTTP(&responseCloseRecorder{httptest.NewRecorder(), conn}, req)
			reader := bufio.NewReader(client)
			_, err := http.ReadResponse(reader, req)
			require.NoError(t, err)
			_, err = io.WriteString(client, "GET / HTTP/1.1\r\nHost: example.test\r\n\r\n")
			require.NoError(t, err)
			resp, err := http.ReadResponse(reader, nil)
			if err == nil {
				var data []byte
				data, err = io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if tc.failAt == 0 {
					assert.Equal(t, "ok", string(data))
				}
			}
			if tc.failAt == 0 {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			ctx := <-contexts
			select {
			case <-ctx.Done(): // Request cancellation runs after deferred body cleanup.
			case <-time.After(5 * time.Second):
				t.Fatal("proxy did not finish the request")
			}
			assert.Equal(t, 1, body.closes)
			if tc.wantErr != nil {
				assert.Contains(t, logs.String(), tc.wantErr.Error())
			}
		})
	}
}
