package goproxy_test

import (
	"context"
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
