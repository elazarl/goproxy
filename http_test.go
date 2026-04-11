package goproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type lateTrailerBody struct {
	io.ReadCloser
	onClose func()
}

func (b *lateTrailerBody) Close() error {
	if b.onClose != nil {
		b.onClose()
		b.onClose = nil
	}
	return b.ReadCloser.Close()
}

func TestHandleHTTPWritesDeclaredTrailers(t *testing.T) {
	proxy := NewProxyHttpServer()
	proxy.OnRequest().DoFunc(func(r *http.Request, _ *ProxyCtx) (*http.Request, *http.Response) {
		trailer := http.Header{}
		body := &lateTrailerBody{
			ReadCloser: io.NopCloser(strings.NewReader("hello")),
			onClose: func() {
				trailer.Add("Server-Timing", "server_read;dur=1")
			},
		}

		return r, &http.Response{
			Status:        "200 OK",
			StatusCode:    http.StatusOK,
			Header:        http.Header{"Content-Length": []string{"5"}, "Content-Type": []string{"text/plain"}, "Trailer": []string{"Server-Timing"}},
			Trailer:       trailer,
			Body:          body,
			ContentLength: 5,
			Request:       r,
		}
	})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/trailers", nil)
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	resp := rec.Result()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close response body: %v", err)
	}

	if got := string(body); got != "hello" {
		t.Fatalf("unexpected body %q", got)
	}
	if got := resp.Trailer.Get("Server-Timing"); got != "server_read;dur=1" {
		t.Fatalf("unexpected trailer %q", got)
	}
	if resp.ContentLength != -1 {
		t.Fatalf("expected chunked response for trailers, got content length %d", resp.ContentLength)
	}
}
