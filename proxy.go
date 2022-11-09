package goproxy

import (
	"bufio"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
)

// The basic proxy type. Implements http.Handler.
type ProxyHttpServer struct {
	// session variable must be aligned in i386
	// see http://golang.org/src/pkg/sync/atomic/doc.go#L41
	sess int64
	// KeepDestinationHeaders indicates the proxy should retain any headers present in the http.Response before proxying
	KeepDestinationHeaders bool
	// setting Verbose to true will log information on each request sent to the proxy
	Verbose         bool
	Logger          Logger
	NonproxyHandler http.Handler
	reqHandlers     []ReqHandler
	respHandlers    []RespHandler
	httpsHandlers   []HttpsHandler
	Tr              *http.Transport
	// Will be invoked to return a custom response to clients when goproxy fails to connect to a proxy target
	HTTPErrorHandler func(io.WriteCloser, *ProxyCtx, error)
	// ConnectDial will be used to create TCP connections for CONNECT requests
	// if nil Tr.Dial will be used
	ConnectDial        func(network string, addr string) (net.Conn, error)
	ConnectDialWithReq func(req *http.Request, network string, addr string) (net.Conn, error)
	CertStore          CertStorage
	KeepHeader         bool
}

var hasPort = regexp.MustCompile(`:\d+$`)

func copyHeaders(dst, src http.Header, keepDestHeaders bool) {
	if !keepDestHeaders {
		for k := range dst {
			dst.Del(k)
		}
	}
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

func isEof(r *bufio.Reader) bool {
	_, err := r.Peek(1)
	if err == io.EOF {
		return true
	}
	return false
}

func (proxy *ProxyHttpServer) filterRequest(r *http.Request, ctx *ProxyCtx) (req *http.Request, resp *http.Response) {
	req = r
	for _, h := range proxy.reqHandlers {
		req, resp = h.Handle(r, ctx)
		// non-nil resp means the handler decided to skip sending the request
		// and return canned response instead.
		if resp != nil {
			break
		}
	}
	return
}
func (proxy *ProxyHttpServer) filterResponse(respOrig *http.Response, ctx *ProxyCtx) (resp *http.Response) {
	resp = respOrig
	for _, h := range proxy.respHandlers {
		ctx.Resp = resp
		resp = h.Handle(resp, ctx)
	}
	return
}

func removeProxyHeaders(ctx *ProxyCtx, r *http.Request) {
	r.RequestURI = "" // this must be reset when serving a request with the client
	ctx.Logf("Sending request %v %v", r.Method, r.URL.String())
	// If no Accept-Encoding header exists, Transport will add the headers it can accept
	// and would wrap the response body with the relevant reader.
	r.Header.Del("Accept-Encoding")
	// curl can add that, see
	// https://jdebp.eu./FGA/web-proxy-connection-header.html
	r.Header.Del("Proxy-Connection")
	r.Header.Del("Proxy-Authenticate")
	r.Header.Del("Proxy-Authorization")
	// Connection, Authenticate and Authorization are single hop Header:
	// http://www.w3.org/Protocols/rfc2616/rfc2616.txt
	// 14.10 Connection
	//   The Connection general-header field allows the sender to specify
	//   options that are desired for that particular connection and MUST NOT
	//   be communicated by proxies over further connections.

	// When server reads http request it sets req.Close to true if
	// "Connection" header contains "close".
	// https://github.com/golang/go/blob/master/src/net/http/request.go#L1080
	// Later, transfer.go adds "Connection: close" back when req.Close is true
	// https://github.com/golang/go/blob/master/src/net/http/transfer.go#L275
	// That's why tests that checks "Connection: close" removal fail
	if r.Header.Get("Connection") == "close" {
		r.Close = false
	}
	r.Header.Del("Connection")
}

type flushWriter struct {
	w io.Writer
}

func (fw flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if f, ok := fw.w.(http.Flusher); ok {
		// only flush if the Writer implements the Flusher interface.
		f.Flush()
	}

	return n, err
}

// Standard net/http function. Shouldn't be used directly, http.Serve will use it.
func (proxy *ProxyHttpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	//r.Header["X-Forwarded-For"] = w.RemoteAddr()
	if r.Method == "CONNECT" {
		proxy.handleHttps(w, r)
	} else {
		proxy.handleHttp(w, r)
	}
}

// NewProxyHttpServer creates and returns a proxy server, logging to stderr by default
func NewProxyHttpServer() *ProxyHttpServer {
	proxy := ProxyHttpServer{
		Logger:        log.New(os.Stderr, "", log.LstdFlags),
		reqHandlers:   []ReqHandler{},
		respHandlers:  []RespHandler{},
		httpsHandlers: []HttpsHandler{},
		NonproxyHandler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			http.Error(w, "This is a proxy server. Does not respond to non-proxy requests.", 500)
		}),
		Tr: &http.Transport{TLSClientConfig: tlsClientSkipVerify, Proxy: http.ProxyFromEnvironment},
	}

	proxy.ConnectDial = dialerFromEnv(&proxy)

	return &proxy
}
