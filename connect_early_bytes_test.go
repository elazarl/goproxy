package goproxy_test

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/elazarl/goproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clientHelloRecord returns the first TLS record a client sends: its ClientHello.
func clientHelloRecord(t *testing.T, serverName string) []byte {
	t.Helper()

	a, b := net.Pipe()
	defer func() { _ = a.Close() }()
	defer func() { _ = b.Close() }()
	go func() {
		_ = tls.Client(a, &tls.Config{ServerName: serverName, InsecureSkipVerify: true}).Handshake()
	}()
	require.NoError(t, b.SetReadDeadline(time.Now().Add(5*time.Second)))
	head := make([]byte, 5)
	_, err := io.ReadFull(b, head)
	require.NoError(t, err)
	body := make([]byte, int(head[3])<<8|int(head[4]))
	_, err = io.ReadFull(b, body)
	require.NoError(t, err)
	return append(head, body...)
}

// A client may send its first bytes in the same write as the CONNECT request (a TLS ClientHello, for
// instance). They are read off the socket into the HTTP server's buffer while the request is parsed, and
// the tunnel must start with them.
func TestConnectEarlyClientBytes(t *testing.T) {
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(k, "")
	}
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer backend.Close()

	proxy := goproxy.NewProxyHttpServer()
	proxyServer := httptest.NewServer(proxy)
	defer proxyServer.Close()

	backendURL, err := url.Parse(backend.URL)
	require.NoError(t, err)
	proxyURL, err := url.Parse(proxyServer.URL)
	require.NoError(t, err)

	conn, err := net.Dial("tcp", proxyURL.Host)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))

	// The CONNECT request and the ClientHello in one write
	connect := "CONNECT " + backendURL.Host + " HTTP/1.1\r\nHost: " + backendURL.Host + "\r\n\r\n"
	_, err = conn.Write(append([]byte(connect), clientHelloRecord(t, "example.com")...))
	require.NoError(t, err)

	head := readHTTPHead(t, conn)
	require.True(t, strings.HasPrefix(head, "HTTP/1.1 200") || strings.HasPrefix(head, "HTTP/1.0 200"), head)

	// The server answers the ClientHello with a ServerHello: a TLS handshake record
	b := make([]byte, 1)
	_, err = io.ReadFull(conn, b)
	require.NoError(t, err, "no answer to the ClientHello sent with the CONNECT request")
	assert.Equal(t, byte(0x16), b[0])
}

// The MITM path has the same early-bytes problem: after answering CONNECT the
// proxy performs a TLS handshake, but a ClientHello sent in the same write as
// the CONNECT request is still in the hijacked reader and must be replayed there
// too.
func TestMitmConnectEarlyClientBytes(t *testing.T) {
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(k, "")
	}
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer backend.Close()

	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxyServer := httptest.NewServer(proxy)
	defer proxyServer.Close()

	backendURL, err := url.Parse(backend.URL)
	require.NoError(t, err)
	proxyURL, err := url.Parse(proxyServer.URL)
	require.NoError(t, err)

	conn, err := net.Dial("tcp", proxyURL.Host)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))

	// The CONNECT request and the ClientHello in one write
	connect := "CONNECT " + backendURL.Host + " HTTP/1.1\r\nHost: " + backendURL.Host + "\r\n\r\n"
	_, err = conn.Write(append([]byte(connect), clientHelloRecord(t, "example.com")...))
	require.NoError(t, err)

	head := readHTTPHead(t, conn)
	require.True(t, strings.HasPrefix(head, "HTTP/1.1 200") || strings.HasPrefix(head, "HTTP/1.0 200"), head)

	// The proxy answers the ClientHello with a ServerHello: a TLS handshake record
	b := make([]byte, 1)
	_, err = io.ReadFull(conn, b)
	require.NoError(t, err, "no answer to the ClientHello sent with the CONNECT request")
	assert.Equal(t, byte(0x16), b[0])
}
