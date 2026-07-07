package goproxy_test

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elazarl/goproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type countingConn struct {
	net.Conn
	writes *atomic.Int32
}

func (c *countingConn) Write(p []byte) (int, error) {
	c.writes.Add(1)
	return c.Conn.Write(p)
}

type countingListener struct {
	net.Listener
	writes *atomic.Int32
}

func (l *countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return c, err
	}
	return &countingConn{Conn: c, writes: l.writes}, nil
}

// TestMitmResponseHeadIsNotFragmented ensures the MITM response head (status line
// + headers + terminator) is written to the client as a single buffered flush
// rather than one tiny TLS record per header field. Fragmenting it into dozens of
// tiny records breaks strict clients (e.g. tungstenite's WebSocket handshake
// rejects a response delivered as too many small packets).
func TestMitmResponseHeadIsNotFragmented(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, kv := range [][]string{
			{"Cache-Control", "no-cache"},
			{"Content-Language", "en"},
			{"Referrer-Policy", "strict-origin-when-cross-origin"},
			{"Strict-Transport-Security", "max-age=31536000; includeSubDomains"},
			{"Vary", "Accept-Encoding"},
			{"X-Content-Type-Options", "nosniff"},
			{"X-Frame-Options", "DENY"},
			{"X-One", "1"},
			{"X-Two", "2"},
			{"X-Three", "3"},
			{"X-Four", "4"},
			{"X-Five", "5"},
		} {
			w.Header().Set(kv[0], kv[1])
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	proxy := goproxy.NewProxyHttpServer()
	proxy.Tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	proxy.OnRequest(goproxy.ReqHostIs(upstream.Listener.Addr().String())).HandleConnect(goproxy.AlwaysMitm)

	var writes atomic.Int32
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := &http.Server{Handler: proxy, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		_ = server.Serve(&countingListener{Listener: ln, writes: &writes})
	}()
	defer server.Close()

	proxyURL := &url.URL{Scheme: "http", Host: ln.Addr().String()}
	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, upstream.URL+"/", nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"))

	// Buffered head flush → a handful of records; unbuffered, the 12-header head alone is dozens.
	n := int(writes.Load())
	t.Logf("client-facing TLS record writes: %d", n)
	assert.Less(t, n, 20,
		"response head should be coalesced into one record, not fragmented per header (got %d writes)", n)
}
