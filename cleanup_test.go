package goproxy_test

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/elazarl/goproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProxyCtxCleanupHttpRunsLIFOAfterClientReceivesResponse(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "11")
		_, _ = w.Write([]byte("export-data"))
	}))
	defer backend.Close()

	clientReadDone := make(chan struct{})
	cleanupFinished := make(chan []string, 1)

	proxy := goproxy.NewProxyHttpServer()
	var events []string
	var mu sync.Mutex
	record := func(ev string) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}

	proxy.OnRequest().DoFunc(func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		ctx.Cleanup(func() {
			record("req-cleanup-1")
			mu.Lock()
			out := append([]string(nil), events...)
			mu.Unlock()
			cleanupFinished <- out
		})
		ctx.Cleanup(func() {
			// Verify the client has already read the complete response before cleanups run.
			<-clientReadDone
			record("req-cleanup-2")
		})
		ctx.Cleanup(nil)
		return r, nil
	})
	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		ctx.Cleanup(func() {
			record("resp-cleanup")
		})
		return resp
	})

	proxyServer := httptest.NewServer(proxy)
	defer proxyServer.Close()

	proxyURL, err := url.Parse(proxyServer.URL)
	require.NoError(t, err)

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
		Timeout: 5 * time.Second,
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, backend.URL+"/export", http.NoBody)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, int64(11), resp.ContentLength)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "export-data", string(body))

	close(clientReadDone)
	assert.Equal(t, []string{"resp-cleanup", "req-cleanup-2", "req-cleanup-1"}, <-cleanupFinished)
}

func TestProxyCtxCleanupRunsOnRoundTripError(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	cleanupRan := make(chan struct{}, 1)

	proxy.OnRequest().DoFunc(func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		ctx.RoundTripper = goproxy.RoundTripperFunc(
			func(_ *http.Request, _ *goproxy.ProxyCtx) (*http.Response, error) {
				return nil, errors.New("upstream unavailable")
			},
		)
		ctx.Cleanup(func() {
			cleanupRan <- struct{}{}
		})
		return r, nil
	})

	proxyServer := httptest.NewServer(proxy)
	defer proxyServer.Close()

	proxyURL, err := url.Parse(proxyServer.URL)
	require.NoError(t, err)

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
		Timeout: 5 * time.Second,
	}

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"http://example.invalid/fail",
		http.NoBody,
	)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	select {
	case <-cleanupRan:
	case <-time.After(2 * time.Second):
		t.Fatal("expected ProxyCtx.Cleanup callback to run on RoundTrip error")
	}
}

func TestProxyCtxCleanupMitmHttp1AndHttp2(t *testing.T) {
	for _, allowHTTP2 := range []bool{false, true} {
		name := "http1"
		if allowHTTP2 {
			name = "http2"
		}
		t.Run(name, func(t *testing.T) {
			backendHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("mitm-ok"))
			})
			backend := httptest.NewUnstartedServer(backendHandler)
			backend.EnableHTTP2 = allowHTTP2
			backend.StartTLS()
			defer backend.Close()

			proxy := goproxy.NewProxyHttpServer()
			proxy.AllowHTTP2 = allowHTTP2
			proxy.Tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
			proxy.Tr.ForceAttemptHTTP2 = allowHTTP2
			proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)

			cleanupOrder := make(chan []int, 1)
			reqHook := func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
				var order []int
				ctx.Cleanup(func() {
					order = append(order, 2)
					cleanupOrder <- order
				})
				ctx.Cleanup(func() {
					order = append(order, 1)
				})
				return r, nil
			}
			proxy.OnRequest().DoFunc(reqHook)

			proxyServer := httptest.NewServer(proxy)
			defer proxyServer.Close()

			proxyURL, err := url.Parse(proxyServer.URL)
			require.NoError(t, err)

			client := &http.Client{
				Transport: &http.Transport{
					Proxy:             http.ProxyURL(proxyURL),
					TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
					ForceAttemptHTTP2: allowHTTP2,
				},
				Timeout: 5 * time.Second,
			}

			req, err := http.NewRequestWithContext(
				context.Background(),
				http.MethodGet,
				backend.URL+"/check",
				http.NoBody,
			)
			require.NoError(t, err)

			resp, err := client.Do(req)
			require.NoError(t, err)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			assert.Equal(t, "mitm-ok", string(body))
			assert.Equal(t, []int{1, 2}, <-cleanupOrder)
		})
	}
}
