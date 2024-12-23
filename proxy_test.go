package goproxy_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/elazarl/goproxy"
)

var (
	https = httptest.NewTLSServer(nil)
	srv   = httptest.NewServer(nil)
	fs    = httptest.NewServer(http.FileServer(http.Dir(".")))
)

type QueryHandler struct{}

func (QueryHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if err := req.ParseForm(); err != nil {
		panic(err)
	}
	_, _ = io.WriteString(w, req.Form.Get("result"))
}

type HeadersHandler struct{}

// This handlers returns a body with a string containing all the request headers it received.
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
	http.DefaultServeMux.Handle("/bobo", ConstantHanlder("bobo"))
	http.DefaultServeMux.Handle("/query", QueryHandler{})
	http.DefaultServeMux.Handle("/headers", HeadersHandler{})
}

type ConstantHanlder string

func (h ConstantHanlder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = io.WriteString(w, string(h))
}

func get(url string, client *http.Client) ([]byte, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	txt, err := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if err != nil {
		return nil, err
	}
	return txt, nil
}

func getOrFail(t *testing.T, url string, client *http.Client) []byte {
	t.Helper()
	txt, err := get(url, client)
	if err != nil {
		t.Fatal("Can't fetch url", url, err)
	}
	return txt
}

func getCert(t *testing.T, c *tls.Conn) []byte {
	t.Helper()
	if err := c.Handshake(); err != nil {
		t.Fatal("cannot handshake", err)
	}
	return c.ConnectionState().PeerCertificates[0].Raw
}

func localFile(url string) string {
	return fs.URL + "/" + url
}

func TestSimpleHttpReqWithProxy(t *testing.T) {
	client, s := oneShotProxy(goproxy.NewProxyHttpServer())
	defer s.Close()

	if r := string(getOrFail(t, srv.URL+"/bobo", client)); r != "bobo" {
		t.Error("proxy server does not serve constant handlers", r)
	}
	if r := string(getOrFail(t, srv.URL+"/bobo", client)); r != "bobo" {
		t.Error("proxy server does not serve constant handlers", r)
	}

	if string(getOrFail(t, https.URL+"/bobo", client)) != "bobo" {
		t.Error("TLS server does not serve constant handlers, when proxy is used")
	}
}

func oneShotProxy(proxy *goproxy.ProxyHttpServer) (client *http.Client, s *httptest.Server) {
	s = httptest.NewServer(proxy)

	proxyUrl, _ := url.Parse(s.URL)
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		Proxy: http.ProxyURL(proxyUrl),
	}
	client = &http.Client{Transport: tr}
	return
}

func TestSimpleHook(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest(goproxy.SrcIpIs("127.0.0.1")).DoFunc(
		func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
			req.URL.Path = "/bobo"
			return req, nil
		},
	)
	client, l := oneShotProxy(proxy)
	defer l.Close()

	if result := string(getOrFail(t, srv.URL+("/momo"), client)); result != "bobo" {
		t.Error("Redirecting all requests from 127.0.0.1 to bobo, didn't work." +
			" (Might break if Go's client sets RemoteAddr to IPv6 address). Got: " +
			result)
	}
}

func TestAlwaysHook(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		req.URL.Path = "/bobo"
		return req, nil
	})
	client, l := oneShotProxy(proxy)
	defer l.Close()

	if result := string(getOrFail(t, srv.URL+("/momo"), client)); result != "bobo" {
		t.Error("Redirecting all requests from 127.0.0.1 to bobo, didn't work." +
			" (Might break if Go's client sets RemoteAddr to IPv6 address). Got: " +
			result)
	}
}

func TestReplaceResponse(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		resp.StatusCode = http.StatusOK
		resp.Body = io.NopCloser(bytes.NewBufferString("chico"))
		return resp
	})

	client, l := oneShotProxy(proxy)
	defer l.Close()

	if result := string(getOrFail(t, srv.URL+("/momo"), client)); result != "chico" {
		t.Error("hooked response, should be chico, instead:", result)
	}
}

func TestReplaceReponseForUrl(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnResponse(goproxy.UrlIs("/koko")).DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		resp.StatusCode = http.StatusOK
		resp.Body = io.NopCloser(bytes.NewBufferString("chico"))
		return resp
	})

	client, l := oneShotProxy(proxy)
	defer l.Close()

	if result := string(getOrFail(t, srv.URL+("/koko"), client)); result != "chico" {
		t.Error("hooked 'koko', should be chico, instead:", result)
	}
	if result := string(getOrFail(t, srv.URL+("/bobo"), client)); result != "bobo" {
		t.Error("still, bobo should stay as usual, instead:", result)
	}
}

func TestOneShotFileServer(t *testing.T) {
	client, l := oneShotProxy(goproxy.NewProxyHttpServer())
	defer l.Close()

	file := "test_data/panda.png"
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal("Cannot find", file)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, fs.URL+"/"+file, nil)
	if err != nil {
		t.Fatal("Cannot create request", err)
	}
	if resp, err := client.Do(req); err == nil {
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal("got", string(b))
		}
		if int64(len(b)) != info.Size() {
			t.Error("Expected Length", file, info.Size(), "actually", len(b), "starts", string(b[:10]))
		}
	} else {
		t.Fatal("Cannot read from fs server", err)
	}
}

func TestContentType(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnResponse(goproxy.ContentTypeIs("image/png")).DoFunc(
		func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
			resp.Header.Set("X-Shmoopi", "1")
			return resp
		},
	)

	client, l := oneShotProxy(proxy)
	defer l.Close()

	for _, file := range []string{"test_data/panda.png", "test_data/football.png"} {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, localFile(file), nil)
		if err != nil {
			t.Fatal("Cannot create request", err)
		}
		if resp, err := client.Do(req); err != nil || resp.Header.Get("X-Shmoopi") != "1" {
			if err == nil {
				t.Error("pngs should have X-Shmoopi header = 1, actually", resp.Header.Get("X-Shmoopi"))
			} else {
				t.Error("error reading png", err)
			}
		}
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, localFile("baby.jpg"), nil)
	if err != nil {
		t.Fatal("Cannot create request", err)
	}
	if resp, err := client.Do(req); err != nil || resp.Header.Get("X-Shmoopi") != "" {
		if err == nil {
			t.Error("Non png images should NOT have X-Shmoopi header at all", resp.Header.Get("X-Shmoopi"))
		} else {
			t.Error("error reading png", err)
		}
	}
}

func panicOnErr(err error, msg string) {
	if err != nil {
		log.Fatal(err.Error() + ":-" + msg)
	}
}

func TestChangeResp(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		_, _ = resp.Body.Read([]byte{0})
		resp.Body = io.NopCloser(new(bytes.Buffer))
		return resp
	})

	client, l := oneShotProxy(proxy)
	defer l.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, localFile("test_data/panda.png"), nil)
	if err != nil {
		t.Fatal("Cannot create request", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body)
	req, err = http.NewRequestWithContext(context.Background(), http.MethodGet, localFile("/bobo"), nil)
	if err != nil {
		t.Fatal("Cannot create request", err)
	}
	_, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSimpleMitm(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest(goproxy.ReqHostIs(https.Listener.Addr().String())).HandleConnect(goproxy.AlwaysMitm)
	proxy.OnRequest(goproxy.ReqHostIs("no such host exists")).HandleConnect(goproxy.AlwaysMitm)

	client, l := oneShotProxy(proxy)
	defer l.Close()

	c, err := tls.Dial("tcp", https.Listener.Addr().String(), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal("cannot dial to tcp server", err)
	}
	origCert := getCert(t, c)
	_ = c.Close()

	c2, err := net.Dial("tcp", l.Listener.Addr().String())
	if err != nil {
		t.Fatal("dialing to proxy", err)
	}
	creq, err := http.NewRequestWithContext(context.Background(), http.MethodConnect, https.URL, nil)
	if err != nil {
		t.Fatal("create new request", creq)
	}
	_ = creq.Write(c2)
	c2buf := bufio.NewReader(c2)
	resp, err := http.ReadResponse(c2buf, creq)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatal("Cannot CONNECT through proxy", err)
	}
	c2tls := tls.Client(c2, &tls.Config{
		InsecureSkipVerify: true,
	})
	proxyCert := getCert(t, c2tls)

	if bytes.Equal(proxyCert, origCert) {
		t.Errorf("Certificate after mitm is not different\n%v\n%v",
			base64.StdEncoding.EncodeToString(origCert),
			base64.StdEncoding.EncodeToString(proxyCert))
	}

	if resp := string(getOrFail(t, https.URL+"/bobo", client)); resp != "bobo" {
		t.Error("Wrong response when mitm", resp, "expected bobo")
	}
	if resp := string(getOrFail(t, https.URL+"/query?result=bar", client)); resp != "bar" {
		t.Error("Wrong response when mitm", resp, "expected bar")
	}
}

func TestMitmMutateRequest(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		// We inject a header in the request
		req.Header.Set("Mitm-Header-Inject", "true")
		return req, nil
	})

	client, l := oneShotProxy(proxy)
	defer l.Close()

	r := string(getOrFail(t, https.URL+"/headers", client))
	if !strings.Contains(r, "Mitm-Header-Inject: true") {
		t.Error("Expected response body to contain the MITM injected header. Got instead: ", r)
	}
}

func TestConnectHandler(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	althttps := httptest.NewTLSServer(ConstantHanlder("althttps"))
	proxy.OnRequest().HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		u, _ := url.Parse(althttps.URL)
		return goproxy.OkConnect, u.Host
	})

	client, l := oneShotProxy(proxy)
	defer l.Close()
	if resp := string(getOrFail(t, https.URL+"/alturl", client)); resp != "althttps" {
		t.Error("Proxy should redirect CONNECT requests to local althttps server, expected 'althttps' got ", resp)
	}
}

func TestMitmIsFiltered(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest(goproxy.ReqHostIs(https.Listener.Addr().String())).HandleConnect(goproxy.AlwaysMitm)
	proxy.OnRequest(goproxy.UrlIs("/momo")).DoFunc(
		func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
			return nil, goproxy.TextResponse(req, "koko")
		},
	)

	client, l := oneShotProxy(proxy)
	defer l.Close()

	if resp := string(getOrFail(t, https.URL+"/momo", client)); resp != "koko" {
		t.Error("Proxy should capture /momo to be koko and not", resp)
	}

	if resp := string(getOrFail(t, https.URL+"/bobo", client)); resp != "bobo" {
		t.Error("But still /bobo should be bobo and not", resp)
	}
}

func TestFirstHandlerMatches(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		return nil, goproxy.TextResponse(req, "koko")
	})
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		panic("should never get here, previous response is no null")
	})

	client, l := oneShotProxy(proxy)
	defer l.Close()

	if resp := string(getOrFail(t, srv.URL+"/", client)); resp != "koko" {
		t.Error("should return always koko and not", resp)
	}
}

func TestIcyResponse(t *testing.T) {
	// TODO: fix this test
	/*s := constantHttpServer([]byte("ICY 200 OK\r\n\r\nblablabla"))
	proxy := goproxy.NewProxyHttpServer()
	proxy.Verbose = true
	_, l := oneShotProxy(proxy, t)
	defer l.Close()
	req, err := http.NewRequest("GET", "http://"+s, nil)
	panicOnErr(err, "newReq")
	proxyip := l.URL[len("http://"):]
	println("got ip: " + proxyip)
	c, err := net.Dial("tcp", proxyip)
	panicOnErr(err, "dial")
	defer c.Close()
	req.WriteProxy(c)
	raw, err := io.ReadAll(c)
	panicOnErr(err, "readAll")
	if string(raw) != "ICY 200 OK\r\n\r\nblablabla" {
		t.Error("Proxy did not send the malformed response received")
	}*/
}

type VerifyNoProxyHeaders struct {
	*testing.T
}

func (v VerifyNoProxyHeaders) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Connection") != "" || r.Header.Get("Proxy-Connection") != "" ||
		r.Header.Get("Proxy-Authenticate") != "" || r.Header.Get("Proxy-Authorization") != "" {
		v.Error("Got Connection header from goproxy", r.Header)
	}
}

func TestNoProxyHeaders(t *testing.T) {
	s := httptest.NewServer(VerifyNoProxyHeaders{t})
	client, l := oneShotProxy(goproxy.NewProxyHttpServer())
	defer l.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, s.URL, nil)
	panicOnErr(err, "bad request")
	req.Header.Add("Proxy-Connection", "close")
	req.Header.Add("Proxy-Authenticate", "auth")
	req.Header.Add("Proxy-Authorization", "auth")
	_, _ = client.Do(req)
}

func TestNoProxyHeadersHttps(t *testing.T) {
	s := httptest.NewTLSServer(VerifyNoProxyHeaders{t})
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	client, l := oneShotProxy(proxy)
	defer l.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, s.URL, nil)
	panicOnErr(err, "bad request")
	req.Header.Add("Proxy-Connection", "close")
	_, _ = client.Do(req)
}

type VerifyAcceptEncodingHeader struct {
	ReceivedHeaderValue string
}

func (v *VerifyAcceptEncodingHeader) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	v.ReceivedHeaderValue = r.Header.Get("Accept-Encoding")
}

func TestAcceptEncoding(t *testing.T) {
	v := VerifyAcceptEncodingHeader{}
	s := httptest.NewServer(&v)
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
			client, l := oneShotProxy(proxy)
			defer l.Close()
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, s.URL, nil)
			panicOnErr(err, "bad request")
			// fully control the Accept-Encoding header we send to the proxy
			tr, ok := client.Transport.(*http.Transport)
			if !ok {
				t.Fatal("invalid client transport")
			}
			tr.DisableCompression = true
			if tc.acceptEncoding != "" {
				req.Header.Add("Accept-Encoding", tc.acceptEncoding)
			}
			_, err = client.Do(req)
			panicOnErr(err, "bad response")
			if v.ReceivedHeaderValue != tc.expectedValue {
				t.Errorf("%+v expected Accept-Encoding: %s, got %s", tc, tc.expectedValue, v.ReceivedHeaderValue)
			}
		})
	}
}

func TestHeadReqHasContentLength(t *testing.T) {
	client, l := oneShotProxy(goproxy.NewProxyHttpServer())
	defer l.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodHead, localFile("test_data/panda.png"), nil)
	if err != nil {
		t.Fatal("Cannot create request", err)
	}

	resp, err := client.Do(req)
	panicOnErr(err, "resp to HEAD")
	if resp.Header.Get("Content-Length") == "" {
		t.Error("Content-Length should exist on HEAD requests")
	}
}

func TestChunkedResponse(t *testing.T) {
	l, err := net.Listen("tcp", ":10234")
	panicOnErr(err, "listen")
	defer l.Close()
	go func() {
		for i := 0; i < 2; i++ {
			c, err := l.Accept()
			panicOnErr(err, "accept")
			_, err = http.ReadRequest(bufio.NewReader(c))
			panicOnErr(err, "readrequest")
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

	c, err := net.Dial("tcp", "localhost:10234")
	panicOnErr(err, "dial")
	defer c.Close()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	_ = req.Write(c)
	resp, err := http.ReadResponse(bufio.NewReader(c), req)
	panicOnErr(err, "readresp")
	b, err := io.ReadAll(resp.Body)
	panicOnErr(err, "readall")
	expected := "This is the data in the first chunk\r\nand this is the second one\r\nconsequence"
	if string(b) != expected {
		t.Errorf("Got `%v` expected `%v`", string(b), expected)
	}

	proxy := goproxy.NewProxyHttpServer()
	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		panicOnErr(ctx.Error, "error reading output")
		b, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		panicOnErr(err, "readall onresp")
		if enc := resp.Header.Get("Transfer-Encoding"); enc != "" {
			t.Fatal("Chunked response should be received as plaintext", enc)
		}
		resp.Body = io.NopCloser(bytes.NewBufferString(strings.ReplaceAll(string(b), "e", "E")))
		return resp
	})

	client, s := oneShotProxy(proxy)
	defer s.Close()

	req, err = http.NewRequestWithContext(context.Background(), http.MethodGet, "http://localhost:10234/", nil)
	if err != nil {
		t.Fatal("Cannot create request", err)
	}

	resp, err = client.Do(req)
	panicOnErr(err, "client.Get")
	b, err = io.ReadAll(resp.Body)
	panicOnErr(err, "readall proxy")
	if string(b) != strings.ReplaceAll(expected, "e", "E") {
		t.Error("expected", expected, "w/ e->E. Got", string(b))
	}
}

func TestGoproxyThroughProxy(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy2 := goproxy.NewProxyHttpServer()
	doubleString := func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		b, err := io.ReadAll(resp.Body)
		panicOnErr(err, "readAll resp")
		resp.Body = io.NopCloser(bytes.NewBufferString(string(b) + " " + string(b)))
		return resp
	}
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.OnResponse().DoFunc(doubleString)

	_, l := oneShotProxy(proxy)
	defer l.Close()

	proxy2.ConnectDial = proxy2.NewConnectDialToProxy(l.URL)

	client, l2 := oneShotProxy(proxy2)
	defer l2.Close()
	if r := string(getOrFail(t, https.URL+"/bobo", client)); r != "bobo bobo" {
		t.Error("Expected bobo doubled twice, got", r)
	}
}

func TestHttpProxyAddrsFromEnv(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	doubleString := func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		b, err := io.ReadAll(resp.Body)
		panicOnErr(err, "readAll resp")
		resp.Body = io.NopCloser(bytes.NewBufferString(string(b) + " " + string(b)))
		return resp
	}
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.OnResponse().DoFunc(doubleString)

	_, l := oneShotProxy(proxy)
	defer l.Close()

	t.Setenv("https_proxy", l.URL)
	proxy2 := goproxy.NewProxyHttpServer()

	client, l2 := oneShotProxy(proxy2)
	defer l2.Close()
	if r := string(getOrFail(t, https.URL+"/bobo", client)); r != "bobo bobo" {
		t.Error("Expected bobo doubled twice, got", r)
	}
}

func TestGoproxyHijackConnect(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest(goproxy.ReqHostIs(srv.Listener.Addr().String())).
		HijackConnect(func(req *http.Request, client net.Conn, ctx *goproxy.ProxyCtx) {
			t.Logf("URL %+#v\nSTR %s", req.URL, req.URL.String())
			getReq, err := http.NewRequestWithContext(req.Context(), http.MethodGet, (&url.URL{
				Scheme: "http",
				Host:   req.URL.Host,
				Path:   "/bobo",
			}).String(), nil)
			if err != nil {
				t.Fatal("Cannot create request", err)
			}
			httpClient := &http.Client{}
			resp, err := httpClient.Do(getReq)
			panicOnErr(err, "http.Get(CONNECT url)")
			panicOnErr(resp.Write(client), "resp.Write(client)")
			_ = resp.Body.Close()
			_ = client.Close()
		})
	client, l := oneShotProxy(proxy)
	defer l.Close()
	proxyAddr := l.Listener.Addr().String()
	conn, err := net.Dial("tcp", proxyAddr)
	panicOnErr(err, "conn "+proxyAddr)
	buf := bufio.NewReader(conn)
	writeConnect(conn)
	if txt := readResponse(buf); txt != "bobo" {
		t.Error("Expected bobo for CONNECT /foo, got", txt)
	}

	if r := string(getOrFail(t, https.URL+"/bobo", client)); r != "bobo" {
		t.Error("Expected bobo would keep working with CONNECT", r)
	}
}

func readResponse(buf *bufio.Reader) string {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	panicOnErr(err, "NewRequest")
	resp, err := http.ReadResponse(buf, req)
	panicOnErr(err, "resp.Read")
	defer resp.Body.Close()
	txt, err := io.ReadAll(resp.Body)
	panicOnErr(err, "resp.Read")
	return string(txt)
}

func writeConnect(w io.Writer) {
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
	err := req.Write(w)
	panicOnErr(err, "req(CONNECT).Write")
}

func TestCurlMinusP(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		return goproxy.HTTPMitmConnect, host
	})
	called := false
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		called = true
		return req, nil
	})
	_, l := oneShotProxy(proxy)
	defer l.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "curl", "-p", "-sS", "--proxy", l.URL, srv.URL+"/bobo")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	if output := out.String(); output != "bobo" {
		t.Error("Expected bobo, got", output)
	}
	if !called {
		t.Error("handler not called")
	}
}

func TestSelfRequest(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	_, l := oneShotProxy(proxy)
	defer l.Close()
	if !strings.Contains(string(getOrFail(t, l.URL, &http.Client{})), "non-proxy") {
		t.Fatal("non proxy requests should fail")
	}
}

func TestHasGoproxyCA(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	s := httptest.NewServer(proxy)

	proxyUrl, _ := url.Parse(s.URL)
	goproxyCA := x509.NewCertPool()
	goproxyCA.AddCert(goproxy.GoproxyCa.Leaf)

	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: goproxyCA}, Proxy: http.ProxyURL(proxyUrl)}
	client := &http.Client{Transport: tr}

	if resp := string(getOrFail(t, https.URL+"/bobo", client)); resp != "bobo" {
		t.Error("Wrong response when mitm", resp, "expected bobo")
	}
}

type TestCertStorage struct {
	certs  map[string]*tls.Certificate
	hits   int
	misses int
}

func (tcs *TestCertStorage) Fetch(hostname string, gen func() (*tls.Certificate, error)) (*tls.Certificate, error) {
	var cert *tls.Certificate
	var err error
	cert, ok := tcs.certs[hostname]
	if ok {
		log.Printf("hit %v\n", cert == nil)
		tcs.hits++
	} else {
		cert, err = gen()
		if err != nil {
			return nil, err
		}
		log.Printf("miss %v\n", cert == nil)
		tcs.certs[hostname] = cert
		tcs.misses++
	}
	return cert, err
}

func (tcs *TestCertStorage) statHits() int {
	return tcs.hits
}

func (tcs *TestCertStorage) statMisses() int {
	return tcs.misses
}

func newTestCertStorage() *TestCertStorage {
	tcs := &TestCertStorage{}
	tcs.certs = make(map[string]*tls.Certificate)

	return tcs
}

func TestProxyWithCertStorage(t *testing.T) {
	tcs := newTestCertStorage()
	t.Logf("TestProxyWithCertStorage started")
	proxy := goproxy.NewProxyHttpServer()
	proxy.CertStore = tcs
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		req.URL.Path = "/bobo"
		return req, nil
	})

	s := httptest.NewServer(proxy)

	proxyUrl, _ := url.Parse(s.URL)
	goproxyCA := x509.NewCertPool()
	goproxyCA.AddCert(goproxy.GoproxyCa.Leaf)

	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: goproxyCA}, Proxy: http.ProxyURL(proxyUrl)}
	client := &http.Client{Transport: tr}

	if resp := string(getOrFail(t, https.URL+"/bobo", client)); resp != "bobo" {
		t.Error("Wrong response when mitm", resp, "expected bobo")
	}

	if tcs.statHits() != 0 {
		t.Fatalf("Expected 0 cache hits, got %d", tcs.statHits())
	}
	if tcs.statMisses() != 1 {
		t.Fatalf("Expected 1 cache miss, got %d", tcs.statMisses())
	}

	// Another round - this time the certificate can be loaded
	if resp := string(getOrFail(t, https.URL+"/bobo", client)); resp != "bobo" {
		t.Error("Wrong response when mitm", resp, "expected bobo")
	}

	if tcs.statHits() != 1 {
		t.Fatalf("Expected 1 cache hit, got %d", tcs.statHits())
	}
	if tcs.statMisses() != 1 {
		t.Fatalf("Expected 1 cache miss, got %d", tcs.statMisses())
	}
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

		client, s := oneShotProxy(proxy)
		defer s.Close()

		fullURL := scheme + "://" + tc.Host + tc.RawPath
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, fullURL, nil)
		if err != nil {
			t.Fatal(err)
		}

		if tc.AddOpaque {
			req.URL.Scheme = scheme
			req.URL.Opaque = "//" + tc.Host + tc.RawPath
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		b, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}

		body := string(b)
		if body != "Dummy response" {
			t.Errorf("Expected proxy to return dummy body content but got %s", body)
		}

		if resp.StatusCode != http.StatusAccepted {
			t.Errorf("Expected status: %d, got: %d", http.StatusAccepted, resp.StatusCode)
		}
	}
}

func TestSimpleHttpRequest(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()

	var server *http.Server
	go func() {
		t.Log("serving end proxy server at localhost:5000")
		server = &http.Server{
			Addr:              "localhost:5000",
			Handler:           proxy,
			ReadHeaderTimeout: 10 * time.Second,
		}
		err := server.ListenAndServe()
		if err == nil {
			t.Error("Error shutdown should always return error", err)
		}
	}()

	time.Sleep(1 * time.Second)
	u, _ := url.Parse("http://localhost:5000")
	tr := &http.Transport{
		Proxy: http.ProxyURL(u),
		// Disable HTTP/2.
		TLSNextProto: make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
	}
	client := http.Client{Transport: tr}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com", nil)
	if err != nil {
		t.Fatal("Cannot create request", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Error("Error requesting http site", err)
	} else if resp.StatusCode != http.StatusOK {
		t.Error("Non-OK status requesting http site", err)
	}

	req, err = http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.invalid", nil)
	if err != nil {
		t.Fatal("Cannot create request", err)
	}

	resp, _ = client.Do(req)
	if resp == nil {
		t.Error("No response requesting invalid http site")
	}

	returnNil := func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		return nil
	}
	proxy.OnResponse(goproxy.UrlMatches(regexp.MustCompile(".*"))).DoFunc(returnNil)

	resp, _ = client.Do(req)
	if resp == nil {
		t.Error("No response requesting invalid http site")
	}

	_ = server.Shutdown(context.TODO())
}

func TestResponseContentLength(t *testing.T) {
	// target server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello world"))
	}))
	defer srv.Close()

	// proxy server
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		buf := &bytes.Buffer{}
		buf.WriteString("change")
		resp.Body = io.NopCloser(buf)
		return resp
	})
	proxySrv := httptest.NewServer(proxy)
	defer proxySrv.Close()

	// send request
	client := &http.Client{}
	client.Transport = &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			return url.Parse(proxySrv.URL)
		},
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, _ := client.Do(req)

	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if int64(len(body)) != resp.ContentLength {
		t.Logf("response body: %s", string(body))
		t.Logf("response body Length: %d", len(body))
		t.Logf("response Content-Length: %d", resp.ContentLength)
		t.Fatalf("Wrong response Content-Length.")
	}
}
