package goproxy_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elazarl/goproxy"
)

// strictBody errors (rather than tolerating) a second Close, like response
// bodies from custom RoundTrippers that release resources exactly once.
type strictBody struct {
	io.Reader
	closes atomic.Int32
}

func (b *strictBody) Close() error {
	if b.closes.Add(1) > 1 {
		return errors.New("double close")
	}
	return nil
}

// TestRoundTripperBodyClosedExactlyOnce guards the ctx.RoundTrip body
// wrapping: handleHttp pairs a deferred Close with an explicit Close after
// io.Copy, which double-closed bodies returned by custom RoundTrippers.
func TestRoundTripperBodyClosedExactlyOnce(t *testing.T) {
	body := &strictBody{Reader: strings.NewReader("hello")}

	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		ctx.RoundTripper = goproxy.RoundTripperFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Response, error) {
			return &http.Response{
				Request:       req,
				StatusCode:    http.StatusOK,
				Proto:         "HTTP/1.1",
				ProtoMajor:    1,
				ProtoMinor:    1,
				Header:        http.Header{"Content-Length": []string{"5"}},
				ContentLength: 5,
				Body:          body,
			}, nil
		})
		return req, nil
	})
	proxySrv := httptest.NewServer(proxy)
	defer proxySrv.Close()

	proxyURL, _ := url.Parse(proxySrv.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	resp, err := client.Get("http://example.com/object")
	if err != nil {
		t.Fatalf("request through proxy failed: %v", err)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}
	resp.Body.Close()
	if string(got) != "hello" {
		t.Fatalf("unexpected body: %q", got)
	}
	if n := body.closes.Load(); n != 1 {
		t.Fatalf("round tripper body closed %d times, want exactly 1", n)
	}
}

// TestFilterSwappedBodyBothClosed guards the origBody handling: when a
// response filter replaces the body, both the replacement and the original
// must be closed exactly once.
func TestFilterSwappedBodyBothClosed(t *testing.T) {
	original := &strictBody{Reader: strings.NewReader("original")}
	replacement := &strictBody{Reader: strings.NewReader("replaced")}

	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		ctx.RoundTripper = goproxy.RoundTripperFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Response, error) {
			return &http.Response{
				Request:       req,
				StatusCode:    http.StatusOK,
				Proto:         "HTTP/1.1",
				ProtoMajor:    1,
				ProtoMinor:    1,
				Header:        http.Header{"Content-Length": []string{"8"}},
				ContentLength: 8,
				Body:          original,
			}, nil
		})
		return req, nil
	})
	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		resp.Body.Close()
		resp.Body = replacement
		resp.ContentLength = 8
		return resp
	})
	proxySrv := httptest.NewServer(proxy)
	defer proxySrv.Close()

	proxyURL, _ := url.Parse(proxySrv.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	resp, err := client.Get("http://example.com/object")
	if err != nil {
		t.Fatalf("request through proxy failed: %v", err)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}
	resp.Body.Close()
	if string(got) != "replaced" {
		t.Fatalf("unexpected body: %q", got)
	}
	if n := original.closes.Load(); n != 1 {
		t.Fatalf("original body closed %d times, want exactly 1", n)
	}
	if n := replacement.closes.Load(); n != 1 {
		t.Fatalf("replacement body closed %d times, want exactly 1", n)
	}
}
