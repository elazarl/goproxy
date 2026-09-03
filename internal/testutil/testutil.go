// Package testutil contains helpers shared by the goproxy test suites.
package testutil

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// ConstantHandler is an http.Handler that always writes the same body.
type ConstantHandler string

func (h ConstantHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	_, _ = io.WriteString(w, string(h))
}

// NewProxy serves proxy on an httptest.Server and returns a client that
// routes every request through it. TLS certificate errors are ignored so
// that MITM tests work. The server is closed when the test ends.
func NewProxy(t *testing.T, proxy http.Handler) (*http.Client, *httptest.Server) {
	t.Helper()
	s := httptest.NewServer(proxy)
	t.Cleanup(s.Close)
	return NewProxyClient(t, s.URL), s
}

// NewProxyClient returns an http.Client that routes every request through
// the proxy at proxyURL and ignores TLS certificate errors.
func NewProxyClient(t *testing.T, proxyURL string) *http.Client {
	t.Helper()
	u, err := url.Parse(proxyURL)
	require.NoError(t, err)
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		Proxy:           http.ProxyURL(u),
	}}
}

// Get performs a GET request and returns the response body.
func Get(client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// GetOrFail performs a GET request and fails the test on any error.
func GetOrFail(t *testing.T, client *http.Client, url string) []byte {
	t.Helper()
	body, err := Get(client, url)
	require.NoError(t, err, "cannot fetch %s", url)
	return body
}
