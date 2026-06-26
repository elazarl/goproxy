package goproxy_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/elazarl/goproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
)

func TestHTTP2ConnectFailure(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.AllowHTTP2 = true
	proxy.Tr = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return nil, errors.New("dial-failure")
		},
	}
	proxy.OnRequest().HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		return goproxy.OkConnect, host
	})

	proxySrv := httptest.NewUnstartedServer(proxy)
	proxySrv.EnableHTTP2 = true
	proxySrv.StartTLS()
	defer proxySrv.Close()

	proxyURL, err := url.Parse(proxySrv.URL)
	require.NoError(t, err)
	conn, err := (&tls.Dialer{
		Config: &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{"h2"},
		},
	}).DialContext(context.Background(), "tcp", proxyURL.Host)
	require.NoError(t, err)
	defer conn.Close()

	tlsConn, ok := conn.(*tls.Conn)
	assert.True(t, ok)
	require.Equal(t, "h2", tlsConn.ConnectionState().NegotiatedProtocol)

	// If we use CONNECT method in a normal *http.Client, it will send an HTTP/1.1 request
	// even if the connection is HTTP/2, so we need to define and use a custom *http2.Transport
	tr := &http2.Transport{}
	client, err := tr.NewClientConn(conn)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodConnect, "https://fake-url.com", nil)
	require.NoError(t, err)

	resp, err := client.RoundTrip(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
}

func TestMitmHTTP2ALPN(t *testing.T) {
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify that we are using HTTP/2
		assert.Equal(t, 2, r.ProtoMajor)
		_, _ = io.WriteString(w, "hello-h2")
	}))
	backend.EnableHTTP2 = true
	backend.StartTLS()
	defer backend.Close()

	proxy := goproxy.NewProxyHttpServer()
	proxy.AllowHTTP2 = true
	proxy.Tr = &http.Transport{
		ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{"h2"},
		},
	}
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)

	reqHandlerCalled := false
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		reqHandlerCalled = true
		return req, nil
	})

	respHandlerCalled := false
	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		resp.Header.Set("X-H2-Resp", "injected-resp")
		respHandlerCalled = true
		return resp
	})

	proxySrv := httptest.NewServer(proxy)
	defer proxySrv.Close()

	client := createProxyClientH2(t, proxySrv.URL)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, backend.URL+"/test", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, "hello-h2", string(body))
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "injected-resp", resp.Header.Get("X-H2-Resp"))
	assert.True(t, reqHandlerCalled, "OnRequest handler should be called")
	assert.True(t, respHandlerCalled, "OnResponse handler should be called")
}

func TestMitmHTTP2ALPNRequestFilter(t *testing.T) {
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("request should have been blocked by the proxy filter")
	}))
	backend.EnableHTTP2 = true
	backend.StartTLS()
	defer backend.Close()

	proxy := goproxy.NewProxyHttpServer()
	proxy.AllowHTTP2 = true
	proxy.Tr = &http.Transport{
		ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{"h2"},
		},
	}
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		return nil, goproxy.TextResponse(req, "blocked-by-proxy")
	})

	proxySrv := httptest.NewServer(proxy)
	defer proxySrv.Close()

	client := createProxyClientH2(t, proxySrv.URL)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, backend.URL+"/any", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, "blocked-by-proxy", string(body))
}

func TestMitmHTTP2ALPNConcurrentStreams(t *testing.T) {
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, html.EscapeString(r.URL.Path))
	}))
	backend.EnableHTTP2 = true
	backend.StartTLS()
	defer backend.Close()

	proxy := goproxy.NewProxyHttpServer()
	proxy.AllowHTTP2 = true
	proxy.Tr = &http.Transport{
		ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{"h2"},
		},
	}
	var connectCalls atomic.Int64
	proxy.OnRequest().HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		connectCalls.Add(1)
		return goproxy.MitmConnect, host
	})

	proxySrv := httptest.NewServer(proxy)
	defer proxySrv.Close()

	client := createProxyClientH2(t, proxySrv.URL)
	ctx := context.Background()

	// Create an initial HTTP/2 request to have only one CONNECT later
	initialReq, err := http.NewRequestWithContext(ctx, http.MethodGet, backend.URL+"/", nil)
	require.NoError(t, err)
	initialResp, err := client.Do(initialReq)
	require.NoError(t, err)
	_ = initialResp.Body.Close()

	type result struct {
		path string
		body string
		err  error
	}

	const iterations = 10
	results := make([][]result, iterations)

	var wg sync.WaitGroup
	paths := []string{"/one", "/two"}
	for i := 0; i < 10; i++ {
		results[i] = make([]result, len(paths))
		for j, path := range paths {
			wg.Add(1)
			go func() {
				defer wg.Done()
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, backend.URL+path, nil)
				if err != nil {
					results[i][j] = result{path, "", err}
					return
				}
				resp, err := client.Do(req)
				if err != nil {
					results[i][j] = result{path, "", err}
					return
				}
				defer func() {
					_ = resp.Body.Close()
				}()
				b, err := io.ReadAll(resp.Body)
				results[i][j] = result{path, string(b), err}
			}()
		}
	}

	wg.Wait()

	for _, iteration := range results {
		for _, r := range iteration {
			require.NoError(t, r.err, "concurrent stream for path '%s' failed", r.path)
			assert.Equal(t, r.path, r.body)
		}
	}

	assert.Equal(t, 1, int(connectCalls.Load()))
}

func TestMitmNoHTTP2WithHTTP1Client(t *testing.T) {
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, 1, r.ProtoMajor)
		assert.Equal(t, 1, r.ProtoMinor)
		_, _ = io.WriteString(w, "h1-response")
	}))
	defer backend.Close()

	proxy := goproxy.NewProxyHttpServer()
	proxy.AllowHTTP2 = true
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.Tr = &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}

	proxySrv := httptest.NewServer(proxy)
	defer proxySrv.Close()

	proxyURL, err := url.Parse(proxySrv.URL)
	require.NoError(t, err)

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				NextProtos:         []string{"http/1.1"},
			},
		},
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, backend.URL+"/", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "h1-response", string(body))
}

func TestMitmProxyHTTP1OriginHTTP2(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "HTTP/2 response")
	})

	// Explicitly make an HTTP/2 server
	srv := httptest.NewUnstartedServer(handler)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	// proxy server
	proxy := goproxy.NewProxyHttpServer()
	proxy.AllowHTTP2 = true
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		// Connection between the proxy client and the proxy server
		assert.Equal(t, "HTTP/1.1", req.Proto)
		return req, nil
	})
	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		// Connection between the proxy server and the origin
		assert.Equal(t, "HTTP/2.0", resp.Proto)
		return resp
	})

	// Configure proxy transport to use HTTP/2 to communicate with the server
	proxy.Tr = &http.Transport{
		ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{"h2"},
		},
	}

	proxySrv := httptest.NewServer(proxy)
	defer proxySrv.Close()

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: func(req *http.Request) (*url.URL, error) {
				return url.Parse(proxySrv.URL)
			},
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	_ = resp.Body.Close()

	assert.Len(t, body, 15)
	assert.Equal(t, "HTTP/1.1", resp.Proto)
	assert.Equal(t, "HTTP/2 response", string(body))
}

func TestMitmH2C(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "HTTP/2.0", r.Proto, "backend should receive h2c request")
		_, _ = io.WriteString(w, "hello-h2c")
	})

	// Use Go 1.24+ UnencryptedHTTP2 for plain-text HTTP/2 (h2c)
	backend := httptest.NewUnstartedServer(handler)
	backend.Config.Protocols = &http.Protocols{}
	backend.Config.Protocols.SetUnencryptedHTTP2(true)
	backend.Start()
	defer backend.Close()

	backendAddr := backend.Listener.Addr().String()

	// Configure proxy to support h2c upstream
	proxy := goproxy.NewProxyHttpServer()
	proxy.AllowHTTP2 = true
	proxy.Tr = &http.Transport{
		Protocols: &http.Protocols{},
	}
	proxy.Tr.Protocols.SetUnencryptedHTTP2(true)
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)

	proxySrv := httptest.NewServer(proxy)
	defer proxySrv.Close()

	uri := url.URL{
		Scheme: "http",
		Host:   backendAddr,
	}

	proxyURL, err := url.Parse(proxySrv.URL)
	require.NoError(t, err)

	conn, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", proxyURL.Host)
	require.NoError(t, err)
	defer conn.Close()

	// Manually establish a single CONNECT tunnel
	connectReq, err := http.NewRequestWithContext(context.Background(), http.MethodConnect, "a.com:443", nil)
	require.NoError(t, err)
	require.NoError(t, connectReq.Write(conn))

	connBuf := bufio.NewReader(conn)
	connectResp, err := http.ReadResponse(connBuf, connectReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, connectResp.StatusCode)

	// Use native UnencryptedHTTP2 (h2c) transport over the already-established tunnel connection
	h2cTr := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			// Return our buffered connection (which may have bytes already in connBuf).
			return &buffConn{Conn: conn, r: connBuf}, nil
		},
		Protocols: &http.Protocols{},
	}
	h2cTr.Protocols.SetUnencryptedHTTP2(true)
	h2cTr.Protocols.SetHTTP1(false)

	h2cClient := &http.Client{
		Transport: h2cTr,
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, uri.String(), nil)
	require.NoError(t, err)

	resp, err := h2cClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "hello-h2c", string(body))
}

// TestMitmHTTP2Coalescing verifies that a single MITM'd HTTP/2 connection can handle
// requests for multiple different hosts (coalescing) correctly.
func TestMitmHTTP2Coalescing(t *testing.T) {
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, html.EscapeString(r.Host))
	}))
	backend.EnableHTTP2 = true
	backend.StartTLS()
	defer backend.Close()

	proxy := goproxy.NewProxyHttpServer()
	proxy.AllowHTTP2 = true
	proxy.Tr = &http.Transport{
		ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{"h2"},
		},
	}

	// Map all outgoing connections to our mock backend
	backendAddr := backend.Listener.Addr().String()
	proxy.Tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, backendAddr)
	}

	var connectCalls atomic.Int64
	proxy.OnRequest().HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		connectCalls.Add(1)
		return goproxy.MitmConnect, host
	})

	proxySrv := httptest.NewServer(proxy)
	defer proxySrv.Close()

	proxyURL, err := url.Parse(proxySrv.URL)
	require.NoError(t, err)

	conn, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", proxyURL.Host)
	require.NoError(t, err)
	defer conn.Close()

	// Manually establish a single CONNECT tunnel
	connectReq, err := http.NewRequestWithContext(context.Background(), http.MethodConnect, "a.com:443", nil)
	require.NoError(t, err)
	require.NoError(t, connectReq.Write(conn))

	resp, err := http.ReadResponse(bufio.NewReader(conn), connectReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Start HTTP/2 session over the tunnel
	tlsConn := tls.Client(conn, &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"h2"},
	})
	require.NoError(t, tlsConn.HandshakeContext(context.Background()))
	tr := &http2.Transport{}
	h2conn, err := tr.NewClientConn(tlsConn)
	require.NoError(t, err)

	// Host "a.com"
	req1, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://a.com/test", nil)
	require.NoError(t, err)
	resp1, err := h2conn.RoundTrip(req1)
	require.NoError(t, err)
	body1, err := io.ReadAll(resp1.Body)
	require.NoError(t, err)
	assert.Equal(t, "a.com", string(body1))

	// Host "b.com", over the same HTTP/2 connection to the proxy
	req2, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://b.com/test", nil)
	require.NoError(t, err)
	resp2, err := h2conn.RoundTrip(req2)
	require.NoError(t, err)
	body2, err := io.ReadAll(resp2.Body)
	require.NoError(t, err)
	assert.Equal(t, "b.com", string(body2))

	assert.Equal(t, 1, int(connectCalls.Load()))
}

func createProxyClientH2(t *testing.T, proxyURL string) *http.Client {
	t.Helper()

	parsedProxyURL, err := url.Parse(proxyURL)
	require.NoError(t, err)

	caPool := x509.NewCertPool()
	caPool.AddCert(goproxy.GoproxyCa.Leaf)

	tr := &http.Transport{
		Proxy:             http.ProxyURL(parsedProxyURL),
		ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{
			RootCAs:            caPool,
			InsecureSkipVerify: true,
			NextProtos:         []string{"h2"},
		},
	}

	return &http.Client{
		Transport: tr,
	}
}

type buffConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *buffConn) Read(b []byte) (int, error) {
	return c.r.Read(b)
}
