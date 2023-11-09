package goproxy

import (
	"bufio"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sync/atomic"

	"golang.org/x/net/http/httpproxy"
)

// The basic proxy type. Implements http.Handler.
type ProxyHttpServer struct {
	// session variable must be aligned in i386
	// see http://golang.org/src/pkg/sync/atomic/doc.go#L41
	sess int64
	// KeepDestinationHeaders indicates the proxy should retain any headers present in the http.Response before proxying
	KeepDestinationHeaders bool
	// KeepAcceptEncoding, if true, prevents the proxy from dropping
	// Accept-Encoding headers from the client.
	//
	// Note that the outbound http.Transport may still choose to add
	// Accept-Encoding: gzip if the client did not explicitly send an
	// Accept-Encoding header. To disable this behavior, set
	// Tr.DisableCompression to true.
	KeepAcceptEncoding bool
	// setting Verbose to true will log information on each request sent to the proxy
	Verbose         bool
	Logger          *log.Logger
	NonproxyHandler http.Handler
	reqHandlers     []ReqHandler
	respHandlers    []RespHandler
	httpsHandlers   []HttpsHandler
	Tr              *http.Transport
	// ConnectDial will be used to create TCP connections for CONNECT requests
	// if nil Tr.Dial will be used
	ConnectDial func(network string, addr string) (net.Conn, error)

	// ConnectDialContext will be used to create TCP connections for CONNECT requests
	// if nil Tr.Dial will be used
	ConnectDialContext func(ctx *ProxyCtx, network string, addr string) (net.Conn, error)

	// ConnectCopyHandler allows users to implement a custom copy routine when forwarding data
	// between the proxy client and proxy target for CONNECT requests
	ConnectCopyHandler func(ctx *ProxyCtx, client, target net.Conn)

	// ConnectClientConnHandler allows users to set a callback function which gets passed
	// the hijacked proxy client net.Conn. This is useful for wrapping the connection
	// to implement timeouts or additional tracing.
	ConnectClientConnHandler func(net.Conn) net.Conn

	// ConnectRespHandler allows users to mutate the response to the CONNECT request before it
	// is returned to the client.
	ConnectRespHandler func(ctx *ProxyCtx, resp *http.Response) error

	// HTTP and HTTPS proxy addresses
	HttpProxyAddr  string
	HttpsProxyAddr string
}

var hasPort = regexp.MustCompile(`:\d+$`)

type ContextKey string

const ProxyContextKey ContextKey = "proxyContext"

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
	if !ctx.proxy.KeepAcceptEncoding {
		// If no Accept-Encoding header exists, Transport will add the headers it can accept
		// and would wrap the response body with the relevant reader.
		r.Header.Del("Accept-Encoding")
	}
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
}

// Standard net/http function. Shouldn't be used directly, http.Serve will use it.
func (proxy *ProxyHttpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	//r.Header["X-Forwarded-For"] = w.RemoteAddr()
	if r.Method == "CONNECT" {
		proxy.handleHttps(w, r)
	} else {
		ctx := &ProxyCtx{Req: r, Session: atomic.AddInt64(&proxy.sess, 1), proxy: proxy}

		var err error
		ctx.Logf("Got request %v %v %v %v", r.URL.Path, r.Host, r.Method, r.URL.String())
		if !r.URL.IsAbs() {
			proxy.NonproxyHandler.ServeHTTP(w, r)
			return
		}
		r, resp := proxy.filterRequest(r, ctx)

		if resp == nil {
			removeProxyHeaders(ctx, r)
			resp, err = ctx.RoundTrip(r)
			if err != nil {
				ctx.Error = err
				resp = proxy.filterResponse(nil, ctx)
				if resp == nil {
					ctx.Logf("error read response %v %v:", r.URL.Host, err.Error())
					http.Error(w, err.Error(), 500)
					return
				}
			}
			ctx.Logf("Received response %v", resp.Status)
		}
		origBody := resp.Body
		resp = proxy.filterResponse(resp, ctx)
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
		ctx.Logf("Copied %v bytes to client error=%v", nr, err)
	}
}

type options struct {
	httpProxyAddr  string
	httpsProxyAddr string
}
type fnOption func(*options)

func (fn fnOption) apply(opts *options) { fn(opts) }

type ProxyHttpServerOptions interface {
	apply(*options)
}

func WithHttpProxyAddr(httpProxyAddr string) ProxyHttpServerOptions {
	return fnOption(func(opts *options) {
		opts.httpProxyAddr = httpProxyAddr
	})
}

func WithHttpsProxyAddr(httpsProxyAddr string) ProxyHttpServerOptions {
	return fnOption(func(opts *options) {
		opts.httpsProxyAddr = httpsProxyAddr
	})
}

// NewProxyHttpServer creates and returns a proxy server, logging to stderr by default
func NewProxyHttpServer(opts ...ProxyHttpServerOptions) *ProxyHttpServer {
	appliedOpts := &options{
		httpProxyAddr: "",
		httpsProxyAddr:  "",
	}
	for _, opt := range opts {
		opt.apply(appliedOpts)
	}

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

	// httpProxyCfg holds configuration for HTTP proxy settings. See FromEnvironment for details.
	httpProxyCfg := httpproxy.FromEnvironment()

	if appliedOpts.httpProxyAddr != "" {
		proxy.HttpProxyAddr = appliedOpts.httpProxyAddr
		httpProxyCfg.HTTPProxy = appliedOpts.httpProxyAddr
	}

	if appliedOpts.httpsProxyAddr != "" {
		proxy.HttpsProxyAddr = appliedOpts.httpsProxyAddr
		httpProxyCfg.HTTPSProxy = appliedOpts.httpsProxyAddr
	}

	proxy.ConnectDial = dialerFromProxy(&proxy)
	
	if appliedOpts.httpProxyAddr != "" || appliedOpts.httpsProxyAddr != "" {
		proxy.Tr.Proxy = func(req *http.Request) (*url.URL, error) {
			return httpProxyCfg.ProxyFunc()(req.URL)
		}
	}

	return &proxy
}
