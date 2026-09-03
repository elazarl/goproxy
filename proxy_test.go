package goproxy_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/elazarl/goproxy"
	"github.com/elazarl/goproxy/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Shared backends for the whole package. srv and https serve http.DefaultServeMux
// (see init), fs serves the repository root so tests can fetch test_data files.
var (
	https = httptest.NewTLSServer(nil)
	srv   = httptest.NewServer(nil)
	fs    = httptest.NewServer(http.FileServer(http.Dir(".")))
)

type QueryHandler struct{}

func (QueryHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	req.Body = http.MaxBytesReader(w, req.Body, 1024*1024)
	if err := req.ParseForm(); err != nil {
		panic(err)
	}
	_, _ = io.WriteString(w, html.EscapeString(req.Form.Get("result")))
}

type HeadersHandler struct{}

// ServeHTTP returns a body listing all the request headers it received.
func (HeadersHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var sb strings.Builder
	for name, values := range req.Header {
		for _, value := range values {
			sb.WriteString(name)
			sb.WriteString(": ")
			sb.WriteString(value)
			sb.WriteString(";")
		}
	}
	_, _ = io.WriteString(w, sb.String())
}

func init() {
	http.DefaultServeMux.Handle("/bobo", testutil.ConstantHandler("bobo"))
	http.DefaultServeMux.Handle("/query", QueryHandler{})
	http.DefaultServeMux.Handle("/headers", HeadersHandler{})
}

func getCert(t *testing.T, c *tls.Conn) []byte {
	t.Helper()
	require.NoError(t, c.HandshakeContext(context.Background()), "cannot handshake")
	return c.ConnectionState().PeerCertificates[0].Raw
}

func localFile(url string) string {
	return fs.URL + "/" + url
}

// doGet performs a GET request through client and fails the test on error.
// The caller must close the response body.
func doGet(t *testing.T, client *http.Client, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
}

// newClientTrustingGoproxyCA returns a client routed through proxyURL that
// only trusts certificates signed by goproxy's built-in CA.
func newClientTrustingGoproxyCA(t *testing.T, proxyURL string) *http.Client {
	t.Helper()
	u, err := url.Parse(proxyURL)
	require.NoError(t, err)
	goproxyCA := x509.NewCertPool()
	goproxyCA.AddCert(goproxy.GoproxyCa.Leaf)
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: goproxyCA},
		Proxy:           http.ProxyURL(u),
	}}
}

func TestSimpleHttpReqWithProxy(t *testing.T) {
	client, _ := testutil.NewProxy(t, goproxy.NewProxyHttpServer())

	assert.Equal(t, "bobo", string(testutil.GetOrFail(t, client, srv.URL+"/bobo")))
	assert.Equal(t, "bobo", string(testutil.GetOrFail(t, client, srv.URL+"/bobo")))
	assert.Equal(t, "bobo", string(testutil.GetOrFail(t, client, https.URL+"/bobo")),
		"TLS server does not serve constant handlers, when proxy is used")
}

func TestSimpleHook(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest(goproxy.SrcIpIs("127.0.0.1")).DoFunc(
		func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
			req.URL.Path = "/bobo"
			return req, nil
		},
	)
	client, _ := testutil.NewProxy(t, proxy)

	assert.Equal(t, "bobo", string(testutil.GetOrFail(t, client, srv.URL+"/momo")),
		"Redirecting all requests from 127.0.0.1 to bobo didn't work "+
			"(might break if Go's client sets RemoteAddr to IPv6 address)")
}

func TestAlwaysHook(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		req.URL.Path = "/bobo"
		return req, nil
	})
	client, _ := testutil.NewProxy(t, proxy)

	assert.Equal(t, "bobo", string(testutil.GetOrFail(t, client, srv.URL+"/momo")))
}

func TestReplaceResponse(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		resp.StatusCode = http.StatusOK
		resp.Body = io.NopCloser(bytes.NewBufferString("chico"))
		return resp
	})
	client, _ := testutil.NewProxy(t, proxy)

	assert.Equal(t, "chico", string(testutil.GetOrFail(t, client, srv.URL+"/momo")))
}

func TestReplaceReponseForUrl(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnResponse(goproxy.UrlIs("/koko")).DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		resp.StatusCode = http.StatusOK
		resp.Body = io.NopCloser(bytes.NewBufferString("chico"))
		return resp
	})
	client, _ := testutil.NewProxy(t, proxy)

	assert.Equal(t, "chico", string(testutil.GetOrFail(t, client, srv.URL+"/koko")))
	assert.Equal(t, "bobo", string(testutil.GetOrFail(t, client, srv.URL+"/bobo")), "bobo should stay as usual")
}

func TestOneShotFileServer(t *testing.T) {
	client, _ := testutil.NewProxy(t, goproxy.NewProxyHttpServer())

	file := "test_data/panda.png"
	info, err := os.Stat(file)
	require.NoError(t, err, "cannot find %s", file)

	b := testutil.GetOrFail(t, client, localFile(file))
	assert.Len(t, b, int(info.Size()))
}

func TestContentType(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnResponse(goproxy.ContentTypeIs("image/png")).DoFunc(
		func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
			resp.Header.Set("X-Shmoopi", "1")
			return resp
		},
	)
	client, _ := testutil.NewProxy(t, proxy)

	for _, file := range []string{"test_data/panda.png", "test_data/football.png"} {
		resp := doGet(t, client, localFile(file))
		_ = resp.Body.Close()
		assert.Equal(t, "1", resp.Header.Get("X-Shmoopi"), "pngs should have X-Shmoopi header = 1")
	}

	resp := doGet(t, client, localFile("baby.jpg"))
	_ = resp.Body.Close()
	assert.Empty(t, resp.Header.Get("X-Shmoopi"), "non png images should NOT have X-Shmoopi header at all")
}

func TestChangeResp(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		_, _ = resp.Body.Read([]byte{0})
		resp.Body = io.NopCloser(new(bytes.Buffer))
		return resp
	})
	client, _ := testutil.NewProxy(t, proxy)

	resp := doGet(t, client, localFile("test_data/panda.png"))
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	resp = doGet(t, client, localFile("/bobo"))
	_ = resp.Body.Close()
}

func TestSimpleMitm(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.Tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	proxy.OnRequest(goproxy.ReqHostIs(https.Listener.Addr().String())).HandleConnect(goproxy.AlwaysMitm)
	proxy.OnRequest(goproxy.ReqHostIs("no such host exists")).HandleConnect(goproxy.AlwaysMitm)
	client, l := testutil.NewProxy(t, proxy)

	ctx := context.Background()
	c, err := (&tls.Dialer{
		Config: &tls.Config{InsecureSkipVerify: true},
	}).DialContext(ctx, "tcp", https.Listener.Addr().String())
	require.NoError(t, err, "cannot dial to tcp server")
	tlsConn, ok := c.(*tls.Conn)
	require.True(t, ok)
	origCert := getCert(t, tlsConn)
	_ = c.Close()

	c2, err := (&net.Dialer{}).DialContext(ctx, "tcp", l.Listener.Addr().String())
	require.NoError(t, err, "dialing to proxy")
	defer c2.Close()
	creq, err := http.NewRequestWithContext(ctx, http.MethodConnect, https.URL, nil)
	require.NoError(t, err)
	require.NoError(t, creq.Write(c2))
	resp, err := http.ReadResponse(bufio.NewReader(c2), creq)
	require.NoError(t, err, "cannot CONNECT through proxy")
	require.Equal(t, http.StatusOK, resp.StatusCode, "cannot CONNECT through proxy")
	c2tls := tls.Client(c2, &tls.Config{InsecureSkipVerify: true})
	proxyCert := getCert(t, c2tls)

	assert.NotEqual(t, origCert, proxyCert, "certificate after mitm is not different")

	assert.Equal(t, "bobo", string(testutil.GetOrFail(t, client, https.URL+"/bobo")))
	assert.Equal(t, "bar", string(testutil.GetOrFail(t, client, https.URL+"/query?result=bar")))
}

func TestMitmMutateRequest(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.Tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		req.Header.Set("Mitm-Header-Inject", "true")
		return req, nil
	})
	client, _ := testutil.NewProxy(t, proxy)

	r := string(testutil.GetOrFail(t, client, https.URL+"/headers"))
	assert.Contains(t, r, "Mitm-Header-Inject: true", "response body should contain the MITM injected header")
}

func TestConnectHandler(t *testing.T) {
	althttps := httptest.NewTLSServer(testutil.ConstantHandler("althttps"))
	defer althttps.Close()

	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		u, _ := url.Parse(althttps.URL)
		return goproxy.OkConnect, u.Host
	})
	client, _ := testutil.NewProxy(t, proxy)

	assert.Equal(t, "althttps", string(testutil.GetOrFail(t, client, https.URL+"/alturl")),
		"proxy should redirect CONNECT requests to local althttps server")
}

func TestMitmIsFiltered(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.Tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	proxy.OnRequest(goproxy.ReqHostIs(https.Listener.Addr().String())).HandleConnect(goproxy.AlwaysMitm)
	proxy.OnRequest(goproxy.UrlIs("/momo")).DoFunc(
		func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
			return nil, goproxy.TextResponse(req, "koko")
		},
	)
	client, _ := testutil.NewProxy(t, proxy)

	assert.Equal(t, "koko", string(testutil.GetOrFail(t, client, https.URL+"/momo")), "proxy should capture /momo")
	assert.Equal(t, "bobo", string(testutil.GetOrFail(t, client, https.URL+"/bobo")), "/bobo should still be bobo")
}

func TestFirstHandlerMatches(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		return nil, goproxy.TextResponse(req, "koko")
	})
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		panic("should never get here, previous response is no null")
	})
	client, _ := testutil.NewProxy(t, proxy)

	assert.Equal(t, "koko", string(testutil.GetOrFail(t, client, srv.URL+"/")))
}

// VerifyNoProxyHeaders fails the test if a hop-by-hop or proxy header reaches the backend.
type VerifyNoProxyHeaders struct {
	t *testing.T
}

func (v VerifyNoProxyHeaders) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	for _, h := range []string{"Connection", "Proxy-Connection", "Proxy-Authenticate", "Proxy-Authorization"} {
		assert.Empty(v.t, r.Header.Get(h), "got %s header from goproxy: %v", h, r.Header)
	}
}

func TestNoProxyHeaders(t *testing.T) {
	s := httptest.NewServer(VerifyNoProxyHeaders{t})
	defer s.Close()
	client, _ := testutil.NewProxy(t, goproxy.NewProxyHttpServer())

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, s.URL, nil)
	require.NoError(t, err)
	req.Header.Add("Proxy-Connection", "close")
	req.Header.Add("Proxy-Authenticate", "auth")
	req.Header.Add("Proxy-Authorization", "auth")
	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
}

func TestNoProxyHeadersHttps(t *testing.T) {
	s := httptest.NewTLSServer(VerifyNoProxyHeaders{t})
	defer s.Close()
	proxy := goproxy.NewProxyHttpServer()
	proxy.Tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	client, _ := testutil.NewProxy(t, proxy)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, s.URL, nil)
	require.NoError(t, err)
	req.Header.Add("Proxy-Connection", "close")
	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
}

type VerifyAcceptEncodingHeader struct {
	ReceivedHeaderValue string
}

func (v *VerifyAcceptEncodingHeader) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	v.ReceivedHeaderValue = r.Header.Get("Accept-Encoding")
}

func TestAcceptEncoding(t *testing.T) {
	v := VerifyAcceptEncodingHeader{}
	s := httptest.NewServer(&v)
	defer s.Close()
	for i, tc := range []struct {
		keepAcceptEncoding bool
		disableCompression bool
		acceptEncoding     string
		expectedValue      string
	}{
		{false, false, "", "gzip"},
		{false, false, "identity", "gzip"},
		{false, true, "", ""},
		{false, true, "identity", ""},
		{true, false, "", "gzip"},
		{true, false, "identity", "identity"},
		{true, true, "", ""},
		{true, true, "identity", "identity"},
	} {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			proxy := goproxy.NewProxyHttpServer()
			proxy.KeepAcceptEncoding = tc.keepAcceptEncoding
			proxy.Tr.DisableCompression = tc.disableCompression
			client, _ := testutil.NewProxy(t, proxy)
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, s.URL, nil)
			require.NoError(t, err)
			// fully control the Accept-Encoding header we send to the proxy
			tr, ok := client.Transport.(*http.Transport)
			require.True(t, ok, "invalid client transport")
			tr.DisableCompression = true
			if tc.acceptEncoding != "" {
				req.Header.Add("Accept-Encoding", tc.acceptEncoding)
			}
			resp, err := client.Do(req)
			require.NoError(t, err)
			_ = resp.Body.Close()
			assert.Equal(t, tc.expectedValue, v.ReceivedHeaderValue, "%+v", tc)
		})
	}
}

func TestHeadReqHasContentLength(t *testing.T) {
	client, _ := testutil.NewProxy(t, goproxy.NewProxyHttpServer())

	req, err := http.NewRequestWithContext(context.Background(), http.MethodHead, localFile("test_data/panda.png"), nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.NotEmpty(t, resp.Header.Get("Content-Length"), "Content-Length should exist on HEAD requests")
}

// TestChunkedResponse checks that a chunked upstream body is de-chunked before
// it reaches OnResponse handlers, and that a modified body is forwarded intact.
func TestChunkedResponse(t *testing.T) {
	ctx := context.Background()

	l, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	go func() {
		for i := 0; i < 2; i++ {
			c, err := l.Accept()
			if !assert.NoError(t, err) {
				return
			}
			_, err = http.ReadRequest(bufio.NewReader(c))
			assert.NoError(t, err)
			_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\n"+
				"Content-Type: text/plain\r\n"+
				"Transfer-Encoding: chunked\r\n\r\n"+
				"25\r\n"+
				"This is the data in the first chunk\r\n\r\n"+
				"1C\r\n"+
				"and this is the second one\r\n\r\n"+
				"3\r\n"+
				"con\r\n"+
				"8\r\n"+
				"sequence\r\n0\r\n\r\n")
			_ = c.Close()
		}
	}()
	upstreamURL := "http://" + l.Addr().String() + "/"

	// Direct request: the raw server speaks chunked encoding correctly.
	c, err := (&net.Dialer{}).DialContext(ctx, "tcp", l.Addr().String())
	require.NoError(t, err)
	defer c.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	require.NoError(t, err)
	require.NoError(t, req.Write(c))
	resp, err := http.ReadResponse(bufio.NewReader(c), req)
	require.NoError(t, err)
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	expected := "This is the data in the first chunk\r\nand this is the second one\r\nconsequence"
	assert.Equal(t, expected, string(b))

	// Errors seen inside the OnResponse handler (proxy goroutine) are checked after the request.
	handlerErr := make(chan error, 1)
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		b, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		handlerErr <- errors.Join(ctx.Error, err)
		assert.Empty(t, resp.Header.Get("Transfer-Encoding"), "chunked response should be received as plaintext")
		resp.Body = io.NopCloser(bytes.NewBufferString(strings.ReplaceAll(string(b), "e", "E")))
		return resp
	})
	client, _ := testutil.NewProxy(t, proxy)

	assert.Equal(t, strings.ReplaceAll(expected, "e", "E"), string(testutil.GetOrFail(t, client, upstreamURL)))
	require.NoError(t, <-handlerErr)
}

// doubleBody is an OnResponse handler that repeats the body twice.
func doubleBody(t *testing.T) func(*http.Response, *goproxy.ProxyCtx) *http.Response {
	t.Helper()
	return func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		b, err := io.ReadAll(resp.Body)
		assert.NoError(t, err) //nolint:testifylint // proxy goroutine; require must not FailNow here
		resp.Body = io.NopCloser(bytes.NewBufferString(string(b) + " " + string(b)))
		return resp
	}
}

func TestGoproxyThroughProxy(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.Tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.OnResponse().DoFunc(doubleBody(t))
	_, l := testutil.NewProxy(t, proxy)

	proxy2 := goproxy.NewProxyHttpServer()
	proxy2.ConnectDial = proxy2.NewConnectDialToProxy(l.URL)
	client, _ := testutil.NewProxy(t, proxy2)

	assert.Equal(t, "bobo bobo", string(testutil.GetOrFail(t, client, https.URL+"/bobo")))
}

func TestHttpProxyAddrsFromEnv(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.Tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.OnResponse().DoFunc(doubleBody(t))
	_, l := testutil.NewProxy(t, proxy)

	t.Setenv("https_proxy", l.URL)
	proxy2 := goproxy.NewProxyHttpServer()
	client, _ := testutil.NewProxy(t, proxy2)

	assert.Equal(t, "bobo bobo", string(testutil.GetOrFail(t, client, https.URL+"/bobo")))
}

func TestGoproxyHijackConnect(t *testing.T) {
	// Error from the hijack handler (proxy goroutine), checked after the request.
	hijackErr := make(chan error, 1)
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest(goproxy.ReqHostIs(srv.Listener.Addr().String())).
		HijackConnect(func(req *http.Request, client net.Conn, ctx *goproxy.ProxyCtx) {
			getReq, err := http.NewRequestWithContext(req.Context(), http.MethodGet, (&url.URL{
				Scheme: "http",
				Host:   req.URL.Host,
				Path:   "/bobo",
			}).String(), nil)
			if err != nil {
				hijackErr <- err
				return
			}
			resp, err := (&http.Client{}).Do(getReq)
			if err != nil {
				hijackErr <- err
				return
			}
			hijackErr <- resp.Write(client)
			_ = resp.Body.Close()
			_ = client.Close()
		})
	client, l := testutil.NewProxy(t, proxy)

	conn, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", l.Listener.Addr().String())
	require.NoError(t, err)
	defer conn.Close()
	buf := bufio.NewReader(conn)
	writeConnect(t, conn)
	assert.Equal(t, "bobo", readResponse(t, buf), "expected bobo for CONNECT /foo")
	require.NoError(t, <-hijackErr)

	assert.Equal(t, "bobo", string(testutil.GetOrFail(t, client, https.URL+"/bobo")),
		"bobo should keep working with CONNECT")
}

func readResponse(t *testing.T, buf *bufio.Reader) string {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, err := http.ReadResponse(buf, req)
	require.NoError(t, err)
	defer resp.Body.Close()
	txt, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(txt)
}

func writeConnect(t *testing.T, w io.Writer) {
	t.Helper()
	// this will let us use IP address of server as url in http.NewRequest by
	// passing it as //127.0.0.1:64584 (prefixed with //).
	// Passing IP address with port alone (without //) will raise error:
	// "first path segment in URL cannot contain colon" more details on this
	// here: https://github.com/golang/go/issues/18824
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: srv.Listener.Addr().String()},
		Host:   srv.Listener.Addr().String(),
		Header: make(http.Header),
	}
	require.NoError(t, req.Write(w))
}

func TestCurlMinusP(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		return goproxy.MitmConnect, host
	})
	called := false
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		called = true
		return req, nil
	})
	_, l := testutil.NewProxy(t, proxy)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "curl", "-p", "-sS", "--proxy", l.URL, srv.URL+"/bobo")
	var out bytes.Buffer
	cmd.Stdout = &out
	require.NoError(t, cmd.Run())

	assert.Equal(t, "bobo", out.String())
	assert.True(t, called, "handler not called")
}

func TestSelfRequest(t *testing.T) {
	_, l := testutil.NewProxy(t, goproxy.NewProxyHttpServer())
	assert.Contains(t, string(testutil.GetOrFail(t, &http.Client{}, l.URL)), "non-proxy",
		"non proxy requests should fail")
}

func TestHasGoproxyCA(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.Tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	_, s := testutil.NewProxy(t, proxy)
	client := newClientTrustingGoproxyCA(t, s.URL)

	assert.Equal(t, "bobo", string(testutil.GetOrFail(t, client, https.URL+"/bobo")))
}

// TestCertStorage is a goproxy.CertStorage that counts cache hits and misses.
type TestCertStorage struct {
	certs  map[string]*tls.Certificate
	hits   int
	misses int
}

func (tcs *TestCertStorage) Fetch(hostname string, gen func() (*tls.Certificate, error)) (*tls.Certificate, error) {
	if cert, ok := tcs.certs[hostname]; ok {
		tcs.hits++
		return cert, nil
	}
	cert, err := gen()
	if err != nil {
		return nil, err
	}
	tcs.certs[hostname] = cert
	tcs.misses++
	return cert, nil
}

func TestProxyWithCertStorage(t *testing.T) {
	tcs := &TestCertStorage{certs: make(map[string]*tls.Certificate)}
	proxy := goproxy.NewProxyHttpServer()
	proxy.Tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	proxy.CertStore = tcs
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		req.URL.Path = "/bobo"
		return req, nil
	})
	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		resp.Close = true
		return resp
	})
	_, s := testutil.NewProxy(t, proxy)
	client := newClientTrustingGoproxyCA(t, s.URL)

	assert.Equal(t, "bobo", string(testutil.GetOrFail(t, client, https.URL+"/bobo")))
	require.Equal(t, 0, tcs.hits, "cache hits after first request")
	require.Equal(t, 1, tcs.misses, "cache misses after first request")

	// Another round - this time the certificate can be loaded from the store
	assert.Equal(t, "bobo", string(testutil.GetOrFail(t, client, https.URL+"/bobo")))
	require.Equal(t, 1, tcs.hits, "cache hits after second request")
	require.Equal(t, 1, tcs.misses, "cache misses after second request")
}

func TestHttpsMitmURLRewrite(t *testing.T) {
	scheme := "https"

	testCases := []struct {
		Host      string
		RawPath   string
		AddOpaque bool
	}{
		{
			Host:      "example.com",
			RawPath:   "/blah/v1/data/realtime",
			AddOpaque: true,
		},
		{
			Host:    "example.com:443",
			RawPath: "/blah/v1/data/realtime?encodedURL=https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fuserinfo.profile",
		},
		{
			Host:    "example.com:443",
			RawPath: "/blah/v1/data/realtime?unencodedURL=https://www.googleapis.com/auth/userinfo.profile",
		},
	}

	for _, tc := range testCases {
		proxy := goproxy.NewProxyHttpServer()
		proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
		proxy.OnRequest(goproxy.DstHostIs(tc.Host)).DoFunc(
			func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
				return nil, goproxy.TextResponse(req, "Dummy response")
			})
		client, _ := testutil.NewProxy(t, proxy)

		fullURL := scheme + "://" + tc.Host + tc.RawPath
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, fullURL, nil)
		require.NoError(t, err)
		if tc.AddOpaque {
			req.URL.Scheme = scheme
			req.URL.Opaque = "//" + tc.Host + tc.RawPath
		}

		resp, err := client.Do(req)
		require.NoError(t, err)
		b, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		require.NoError(t, err)

		assert.Equal(t, "Dummy response", string(b), "%+v", tc)
		assert.Equal(t, http.StatusAccepted, resp.StatusCode, "%+v", tc)
	}
}

// TestSimpleHttpRequest sends real requests to the Internet through the proxy
// and checks that an unresolvable host still yields a response, even when an
// OnResponse handler returns nil.
func TestSimpleHttpRequest(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	_, s := testutil.NewProxy(t, proxy)

	u, err := url.Parse(s.URL)
	require.NoError(t, err)
	client := http.Client{Transport: &http.Transport{
		Proxy: http.ProxyURL(u),
		// Disable HTTP/2.
		TLSNextProto: make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
	}}

	resp := doGet(t, &client, "http://example.com")
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "non-OK status requesting http site")

	resp = doGet(t, &client, "http://example.invalid")
	_ = resp.Body.Close()
	assert.NotNil(t, resp, "no response requesting invalid http site")

	proxy.OnResponse(goproxy.UrlMatches(regexp.MustCompile(".*"))).DoFunc(
		func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
			return nil
		})

	resp = doGet(t, &client, "http://example.invalid")
	_ = resp.Body.Close()
	assert.NotNil(t, resp, "no response requesting invalid http site")
}

func TestResponseContentLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello world"))
	}))
	defer srv.Close()

	proxy := goproxy.NewProxyHttpServer()
	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		resp.Body = io.NopCloser(bytes.NewBufferString("change"))
		return resp
	})
	client, _ := testutil.NewProxy(t, proxy)

	resp := doGet(t, client, srv.URL)
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.NoError(t, err)

	assert.EqualValues(t, len(body), resp.ContentLength, "response body: %s", body)
}

func TestHeaderMultipleValues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Test", "1")
	}))
	defer srv.Close()

	proxy := goproxy.NewProxyHttpServer()
	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		resp.Header.Add("Test", "2")
		return resp
	})
	client, _ := testutil.NewProxy(t, proxy)

	resp := doGet(t, client, srv.URL)
	_ = resp.Body.Close()

	assert.Len(t, resp.Header["Test"], 2)
	assert.Contains(t, resp.Header["Test"], "1")
	assert.Contains(t, resp.Header["Test"], "2")
}

func TestMITMResponseHTTP2MissingContentLength(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			// Force missing Content-Length
			f.Flush()
		}
		_, _ = w.Write([]byte("HTTP/2 response"))
	})

	// Explicitly make an HTTP/2 server
	srv := httptest.NewUnstartedServer(handler)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	proxy := goproxy.NewProxyHttpServer()
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
	client, _ := testutil.NewProxy(t, proxy)

	resp := doGet(t, client, srv.URL)
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.NoError(t, err)

	assert.EqualValues(t, -1, resp.ContentLength)
	assert.Equal(t, []string{"chunked"}, resp.TransferEncoding)
	assert.Len(t, body, 15)
}

func TestMITMResponseContentLength(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.Tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		// Don't touch the body at all
		return resp
	})
	client, _ := testutil.NewProxy(t, proxy)

	resp := doGet(t, client, https.URL+"/bobo")
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.NoError(t, err)

	assert.EqualValues(t, len(body), resp.ContentLength)
}

func TestMITMEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(nil)
	}))
	defer srv.Close()

	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		return resp
	})
	client, _ := testutil.NewProxy(t, proxy)

	resp := doGet(t, client, srv.URL)
	_ = resp.Body.Close()

	assert.EqualValues(t, 0, resp.ContentLength)
}

func TestMITMNoContentResponse(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	proxy := goproxy.NewProxyHttpServer()
	proxy.Tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	client, _ := testutil.NewProxy(t, proxy)

	resp := doGet(t, client, srv.URL)
	_ = resp.Body.Close()

	assert.Equal(t, http.StatusNotModified, resp.StatusCode)
	assert.NotContains(t, resp.TransferEncoding, "chunked")
}

func TestMITMOverwriteAlreadyEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(nil)
	}))
	defer srv.Close()

	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		assert.EqualValues(t, 0, resp.ContentLength)
		resp.Body = io.NopCloser(bytes.NewReader(nil))
		return resp
	})
	client, _ := testutil.NewProxy(t, proxy)

	resp := doGet(t, client, srv.URL)
	_ = resp.Body.Close()

	assert.EqualValues(t, 0, resp.ContentLength)
}

func TestMITMOverwriteBodyToEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("test"))
	}))
	defer srv.Close()

	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		assert.EqualValues(t, 4, resp.ContentLength)
		resp.Body = io.NopCloser(bytes.NewReader(nil))
		return resp
	})
	client, _ := testutil.NewProxy(t, proxy)

	resp := doGet(t, client, srv.URL)
	_ = resp.Body.Close()

	assert.EqualValues(t, 0, resp.ContentLength)
}

func TestMITMRequestCancel(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello world"))
	}))
	defer srv.Close()

	proxy := goproxy.NewProxyHttpServer()
	proxy.Tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	var request *http.Request
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		request = req
		return req, nil
	})
	client, _ := testutil.NewProxy(t, proxy)

	resp := doGet(t, client, srv.URL)
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.NoError(t, err)

	assert.Equal(t, "hello world", string(body))
	require.NotNil(t, request)

	select {
	case _, ok := <-request.Context().Done():
		assert.False(t, ok)
	default:
		assert.Fail(t, "request hasn't been cancelled")
	}
}

func TestNewResponseProtoVersion(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/", nil)
	require.NoError(t, err)

	resp := goproxy.NewResponse(req, goproxy.ContentTypeText, http.StatusForbidden, "blocked")

	assert.Equal(t, "HTTP/1.1", resp.Proto)
	assert.Equal(t, 1, resp.ProtoMajor)
	assert.Equal(t, 1, resp.ProtoMinor)

	var buf bytes.Buffer
	require.NoError(t, resp.Write(&buf))

	line, err := buf.ReadString('\n')
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(line, "HTTP/1.1 403"), "expected HTTP/1.1 status line, got: %s", line)
}

func TestNewResponseMitmWrite(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		return nil, goproxy.NewResponse(req, goproxy.ContentTypeText, http.StatusForbidden, "blocked")
	})
	client, _ := testutil.NewProxy(t, proxy)

	resp := doGet(t, client, https.URL+"/anything")
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, "blocked", string(body))
}

func TestPersistentMitmRequest(t *testing.T) {
	requestCount := 0
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "Request number %d", requestCount)
		requestCount++
	}))
	defer backend.Close()

	proxy := goproxy.NewProxyHttpServer()
	proxy.Tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxyServer := httptest.NewServer(proxy)
	defer proxyServer.Close()

	proxyURL, err := url.Parse(proxyServer.URL)
	require.NoError(t, err)

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
			// We disable HTTP/2 to make sure to test HTTP/1.1 Keep-Alive
			ForceAttemptHTTP2: false,
		},
	}

	for i := 0; i < 2; i++ {
		var connReused bool
		trace := &httptrace.ClientTrace{
			GotConn: func(info httptrace.GotConnInfo) {
				connReused = info.Reused
			},
		}

		ctx := httptrace.WithClientTrace(context.Background(), trace)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, backend.URL, nil)
		require.NoError(t, err)

		resp, err := client.Do(req)
		require.NoError(t, err)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		_ = resp.Body.Close()

		assert.Equal(t, fmt.Sprintf("Request number %d", i), string(body))

		// First request creates the connection, second request reuses it
		switch i {
		case 0:
			assert.False(t, connReused)
		case 1:
			assert.True(t, connReused)
		}
	}
}

func TestMITMResponseHTTP2ProtoVersion(t *testing.T) {
	// Upstream HTTP/2 server
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello"))
	})
	srv := httptest.NewUnstartedServer(handler)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	// Proxy with MITM and HTTP/2 upstream transport
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.Tr = &http.Transport{
		ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{"h2"},
		},
	}
	_, proxySrv := testutil.NewProxy(t, proxy)

	// Client talks HTTP/1.1 through the MITM proxy
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", proxySrv.Listener.Addr().String())
	require.NoError(t, err)
	defer conn.Close()

	// Send CONNECT
	connectReq, err := http.NewRequestWithContext(context.Background(), http.MethodConnect, srv.URL, nil)
	require.NoError(t, err)
	require.NoError(t, connectReq.Write(conn))
	br := bufio.NewReader(conn)
	connectResp, err := http.ReadResponse(br, connectReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, connectResp.StatusCode)

	// TLS handshake with the MITM'd proxy
	tlsConn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true})
	require.NoError(t, tlsConn.HandshakeContext(context.Background()))

	// Send an HTTP/1.1 request through the tunnel
	httpReq, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/test", nil)
	require.NoError(t, err)
	require.NoError(t, httpReq.Write(tlsConn))

	// Read response — must be HTTP/1.x, not HTTP/2.0
	resp, err := http.ReadResponse(bufio.NewReader(tlsConn), httpReq)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(body))
	assert.Equal(t, 1, resp.ProtoMajor,
		"MITM'd client should receive HTTP/1.x response, got %s", resp.Proto)
}

// TestTrailersForwarded verifies that response trailers (e.g. gRPC's
// grpc-status, grpc-message) emitted by the upstream server are forwarded
// through the proxy to the client.
//
// Regression test for https://github.com/elazarl/goproxy/issues/408
// ("Proxying grpc/h2c requests fail with 'server closed the stream
// without sending trailers'").
func TestTrailersForwarded(t *testing.T) {
	const (
		bodyText     = "hello world"
		announcedKey = "Grpc-Status"
		announcedVal = "0"
		unannouncedK = "X-Late-Trailer"
		unannouncedV = "abc"
	)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Pre-announce one trailer (h1 chunked path).
		w.Header().Set("Trailer", announcedKey)
		w.Header().Set("Content-Type", "application/grpc")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, bodyText)
		// Pre-announced: value goes under the unprefixed key.
		w.Header().Set(announcedKey, announcedVal)
		// Late / unannounced: must use TrailerPrefix on the upstream,
		// which httputil-style proxy code paths must forward via
		// TrailerPrefix on the client side too.
		w.Header().Set(http.TrailerPrefix+unannouncedK, unannouncedV)
	}))
	defer upstream.Close()

	client, _ := testutil.NewProxy(t, goproxy.NewProxyHttpServer())

	resp := doGet(t, client, upstream.URL+"/anything")
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, bodyText, string(body))

	// resp.Trailer is only populated after the body is fully consumed.
	require.Equal(t, announcedVal, resp.Trailer.Get(announcedKey),
		"upstream pre-announced trailer should be forwarded by the proxy")
	require.Equal(t, unannouncedV, resp.Trailer.Get(unannouncedK),
		"upstream late/unannounced trailer should be forwarded by the proxy")
}

// TestNoTrailersUnchanged is a sanity check that responses without trailers
// are unaffected by the trailer-forwarding code.
func TestNoTrailersUnchanged(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	client, _ := testutil.NewProxy(t, goproxy.NewProxyHttpServer())

	resp := doGet(t, client, upstream.URL+"/")
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "ok", strings.TrimSpace(string(body)))
	require.Empty(t, resp.Trailer)
}

// TestTransparentTunnelClosesClientConnOnTargetError verifies that when the
// target connection closes unexpectedly during a transparent TCP tunnel
// (ConnectAccept path), the client connection is also closed so the client
// doesn't hang indefinitely.
//
// Regression test for https://github.com/elazarl/goproxy/issues/657
func TestTransparentTunnelClosesClientConnOnTargetError(t *testing.T) {
	// Target server: closes connection immediately after reading from client.
	// This simulates a target IO error (timeout, connection reset).
	targetListener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer targetListener.Close()

	targetConnCh := make(chan net.Conn, 1)
	go func() {
		conn, err := targetListener.Accept()
		if err != nil {
			return
		}
		targetConnCh <- conn
	}()

	// Proxy server: transparent tunnel (ConnectAccept, the default).
	// No HandleConnect handler means OkConnect (transparent tunnel) is used.
	_, proxyServer := testutil.NewProxy(t, goproxy.NewProxyHttpServer())

	// Connect to proxy.
	clientConn, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", proxyServer.Listener.Addr().String())
	require.NoError(t, err)
	defer clientConn.Close()

	// Send CONNECT request.
	connectReq := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: targetListener.Addr().String()},
		Host:   targetListener.Addr().String(),
		Header: make(http.Header),
	}
	require.NoError(t, connectReq.Write(clientConn))

	// Read CONNECT response.
	br := bufio.NewReader(clientConn)
	connectResp, err := http.ReadResponse(br, connectReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, connectResp.StatusCode)

	// Wait for target to accept connection.
	var targetConn net.Conn
	select {
	case targetConn = <-targetConnCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for target connection")
	}

	// Send data through the tunnel so the target starts reading.
	_, err = clientConn.Write([]byte("hello"))
	require.NoError(t, err)

	// Target closes connection (simulates IO error).
	require.NoError(t, targetConn.Close())

	// Verify client connection is closed within timeout.
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 1024)
		_, _ = clientConn.Read(buf)
		close(done)
	}()

	select {
	case <-done:
		// Connection was closed — this is the expected behavior.
	case <-time.After(3 * time.Second):
		t.Fatal("client connection was not closed after target closed; client would hang indefinitely")
	}
}

// TestMitmConnectNormalizesDefaultPortInURL verifies that when a client sends a CONNECT
// request with a default port (e.g., example.com:443), but the inner HTTP/1.1 request
// contains a Host header without the port (e.g., Host: example.com), the proxy
// normalizes req.URL.Host to match the inner Host header.
//
// Regression test for URL normalization during MITM interception.
func TestMitmConnectNormalizesDefaultPortInURL(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()

	var capturedReq *http.Request

	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		capturedReq = req
		// Return a synthetic response to avoid a real network request to example.com
		return nil, goproxy.TextResponse(req, "ok")
	})

	client, l := oneShotProxy(proxy)
	defer l.Close()

	proxyURL, _ := url.Parse(l.URL)
	tr := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		// Forcefully disable HTTP/2 on the client to guarantee the use of the CONNECT method (HTTP/1.1).
		TLSNextProto: make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
	}
	client.Transport = tr

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/bobo", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.NotNil(t, capturedReq, "Request was not captured by the OnRequest hook")
	require.NotNil(t, capturedReq.URL, "capturedReq.URL is nil")

	assert.Equal(t, "example.com", capturedReq.URL.Host,
		"Expected req.URL.Host to be normalized to 'example.com' without ':443'")

	urlStr := capturedReq.URL.String()
	assert.NotContains(t, urlStr, ":443",
		"Expected req.URL.String() to not contain ':443'")
}
