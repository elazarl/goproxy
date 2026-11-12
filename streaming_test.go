package goproxy_test

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/elazarl/goproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHttpChunkedBodyFlushesIncrementally(t *testing.T) {
	allowSecondChunk := make(chan struct{})

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("chunk-1\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		<-allowSecondChunk
		_, _ = w.Write([]byte("chunk-2\n"))
	}))
	defer backend.Close()

	proxy := goproxy.NewProxyHttpServer()
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
		backend.URL+"/stream",
		http.NoBody,
	)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	buf := make([]byte, len("chunk-1\n"))
	_, err = io.ReadFull(resp.Body, buf)
	require.NoError(t, err)
	assert.Equal(t, "chunk-1\n", string(buf))

	close(allowSecondChunk)
	rest, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "chunk-2\n", string(rest))
}

func TestStreamingHeadersFlushedBeforeBody(t *testing.T) {
	for _, useH2 := range []bool{false, true} {
		name := "http"
		if useH2 {
			name = "http2-mitm"
		}
		t.Run(name, func(t *testing.T) {
			allowEvent := make(chan struct{})
			handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_ = http.NewResponseController(w).Flush()

				<-allowEvent
				_, _ = w.Write([]byte("data: ready\n\n"))
			})
			backend := httptest.NewUnstartedServer(handler)
			backend.EnableHTTP2 = useH2
			if useH2 {
				backend.StartTLS()
			} else {
				backend.Start()
			}
			defer backend.Close()

			proxy := goproxy.NewProxyHttpServer()
			proxy.AllowHTTP2 = useH2
			proxy.Tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
			proxy.Tr.ForceAttemptHTTP2 = useH2
			proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
			proxyServer := httptest.NewServer(proxy)
			defer proxyServer.Close()

			proxyURL, err := url.Parse(proxyServer.URL)
			require.NoError(t, err)
			client := &http.Client{
				Transport: &http.Transport{
					Proxy:             http.ProxyURL(proxyURL),
					TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
					ForceAttemptHTTP2: useH2,
				},
				Timeout: 5 * time.Second,
			}

			req, err := http.NewRequestWithContext(
				context.Background(),
				http.MethodGet,
				backend.URL+"/events",
				http.NoBody,
			)
			require.NoError(t, err)

			// Client must receive 200 OK headers before the first SSE event is emitted.
			resp, err := client.Do(req)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			require.Equal(t, http.StatusOK, resp.StatusCode)

			close(allowEvent)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Equal(t, "data: ready\n\n", string(body))
		})
	}
}

func TestHttpHeadBodilessNotChunked(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	proxy := goproxy.NewProxyHttpServer()
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
		http.MethodHead,
		backend.URL+"/events",
		http.NoBody,
	)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, resp.TransferEncoding)
}
