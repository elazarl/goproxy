package goproxy

import (
	"bufio"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"sync"
	"sync/atomic"
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

	// Defined error pages for user
	ErrorPages *ErrorPages

	// ConnectDial will be used to create TCP connections for CONNECT requests
	// if nil Tr.Dial will be used
	ConnectDial func(network string, addr string) (net.Conn, error)
	CertStore   CertStorage
}

var hasPort = regexp.MustCompile(`:\d+$`)

var trPool = sync.Pool{
	New: func() interface{} { return new(http.Transport) },
}

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
	r.Header.Del("Connection")
	// Remove any other proxy headers that may have been added
	for _, header := range ctx.ForwardProxyStripHeaders {
		r.Header.Del(header)
	}
}

// Standard net/http function. Shouldn't be used directly, http.Serve will use it.
func (proxy *ProxyHttpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	//r.Header["X-Forwarded-For"] = w.RemoteAddr()
	if r.Method == "CONNECT" {
		proxy.HandleHttps(w, r, nil)
	} else {

		ctx := &ProxyCtx{Req: r, Session: atomic.AddInt64(&proxy.sess, 1), Proxy: proxy}

		if r == nil || r.URL == nil {
			return
		}

		var err error

		if !r.URL.IsAbs() {
			proxy.NonproxyHandler.ServeHTTP(w, r)
			return
		}

		r, resp := proxy.filterRequest(r, ctx)
		// If a cancel function is set, ensure we call it when
		// we've finished handling the request
		if ctx.Cancel != nil {
			defer ctx.Cancel()
		}

		if r == nil || r.URL == nil {
			return
		}

		ctx.Logf("Got request %v %v %v %v", r.URL.Path, r.Host, r.Method, r.URL.String())

		if resp == nil {
			removeProxyHeaders(ctx, r)
			resp, err = ctx.RoundTrip(r)

			if err != nil {
				ctx.Logf("http roundtrip error %+v", err)
				if ctx.BackupDNSResolver != "" {
					ctx.DNSResolver = ctx.BackupDNSResolver
					ctx.Logf("http retrying with backup resolver %s", ctx.DNSResolver)
					resp, err = ctx.RoundTrip(r)
				}
			}

			if err != nil {
				if ctx.CloseOnError {
					ctx.Logf("http roundtrip error, closing: %+v", err)
					r.Close = true
					return
				}
				ctx.Logf("http roundtrip error %+v", err)
				ctx.Error = err
				resp = proxy.filterResponse(nil, ctx)

			}
			if resp != nil {
				ctx.Logf("Received response %v", resp.Status)
			}
		}
		resp = proxy.filterResponse(resp, ctx)

		if resp == nil {
			var errorString string
			if ctx.Error != nil {
				errorString = "error read response " + r.URL.Host + " : " + ctx.Error.Error()
				ctx.Logf(errorString)
				if proxy.ErrorPages.Enabled() {
					proxy.ErrorPages.WriteErrorPage(ctx.Error, r.URL.Host, w)
				} else {
					http.Error(w, ctx.Error.Error(), 500)
				}
			} else {
				errorString = "error read response " + r.URL.Host
				ctx.Logf(errorString)
				if proxy.ErrorPages.Enabled() {
					proxy.ErrorPages.WriteErrorPage(errors.New(errorString), r.URL.Host, w)
				} else {
					http.Error(w, errorString, 500)
				}
			}
			return
		}
		origBody := resp.Body
		defer origBody.Close()
		ctx.Logf("Copying response to client %v [%d]", resp.Status, resp.StatusCode)
		// http.ResponseWriter will take care of filling the correct response length
		// Setting it now, might impose wrong value, contradicting the actual new
		// body the user returned.
		// We keep the original body to remove the header only if things changed.
		// This will prevent problems with HEAD requests where there's no body, yet,
		// the Content-Length header should be set.
		if origBody != resp.Body {
			resp.Header.Del("Content-Length")
		}
		copyHeaders(w.Header(), resp.Header, proxy.KeepDestinationHeaders)
		w.WriteHeader(resp.StatusCode)
		nr, err := io.Copy(w, resp.Body)
		if err := resp.Body.Close(); err != nil {
			ctx.Warnf("Can't close response body %v", err)
		}
		ctx.BytesReceived += nr
		ctx.Logf("Copied %v bytes to client error=%v", nr, err)
		ctx.Logf("Copied %v bytes from client error=%v", ctx.BytesSent, err)
		if ctx.Tail != nil {
			ctx.Tail(ctx)
		}

	}
}

// NewProxyHttpServer creates and returns a proxy server, logging to stderr by default
func NewProxyHttpServer() *ProxyHttpServer {
	proxy := ProxyHttpServer{
		Logger:        log.New(os.Stderr, "", log.LstdFlags),
		ErrorPages:    &ErrorPages{},
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
