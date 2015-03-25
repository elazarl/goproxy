package goproxy

import (
	"bufio"
	"crypto/tls"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync/atomic"
)

// The basic proxy type. Implements http.Handler.
type ProxyHttpServer struct {
	// session variable must be aligned in i386
	// see http://golang.org/src/pkg/sync/atomic/doc.go#L41
	sess int64
	// setting Verbose to true will log information on each request sent to the proxy
	Verbose bool
	// SniffSNI enables sniffing Server Name Indicator when doing CONNECT calls.  It will thus answer to CONNECT calls with a "200 OK" even if the remote server might not answer.  The result would be the shutdown of the connection instead of an appropriate HTTP error code if the remote node doesn't answer.
	SniffSNI bool
	Logger   *log.Logger

	// Registered handlers
	connectHandlers  []Handler
	requestHandlers  []Handler
	responseHandlers []Handler
	// NonProxyHandler will be used to handle direct connections to the proxy. You can assign an `http.ServeMux` or some other routing libs here.  The default will return a 500 error saying this is a proxy and has nothing to serve by itself.
	NonProxyHandler http.Handler

	// Custom transport to be used
	Transport *http.Transport

	// Setting MITMCertAuth allows you to override the default CA cert/key used to sign MITM'd requests.
	MITMCertAuth *tls.Certificate

	// ConnectDial will be used to create TCP connections for CONNECT requests
	// if nil, .Transport.Dial will be used
	ConnectDial func(network string, addr string) (net.Conn, error)
}

func copyHeaders(dst, src http.Header) {
	for k, _ := range dst {
		dst.Del(k)
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

// Standard net/http function. Shouldn't be used directly, http.Serve will use it.
func (proxy *ProxyHttpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	//r.Header["X-Forwarded-For"] = w.RemoteAddr()

	ctx := &ProxyCtx{
		Method:         r.Method,
		SourceIP:       r.RemoteAddr, // pick it from somewhere else ? have a plugin to override this ?
		Req:            r,
		ResponseWriter: w,
		UserData:       make(map[string]interface{}),
		Session:        atomic.AddInt64(&proxy.sess, 1),
		proxy:          proxy,
		MITMCertAuth:   proxy.MITMCertAuth,
	}
	ctx.host = r.URL.Host

	if r.Method == "CONNECT" {
		proxy.dispatchConnectHandlers(ctx)
		//proxy.handleHttpsConnect(w, r)
	} else {
		ctx.Logf("Got request %v %v %v %v", r.URL.Path, r.Host, r.Method, r.URL.String())
		if !r.URL.IsAbs() {
			proxy.NonProxyHandler.ServeHTTP(w, r)
			return
		}

		proxy.dispatchRequestHandlers(ctx)
	}
}

// New proxy server, logs to StdErr by default
func NewProxyHttpServer() *ProxyHttpServer {
	proxy := ProxyHttpServer{
		Logger:           log.New(os.Stderr, "", log.LstdFlags),
		requestHandlers:  []Handler{},
		responseHandlers: []Handler{},
		connectHandlers:  []Handler{},
		NonProxyHandler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			http.Error(w, "This is a proxy server. Does not respond to non-proxy requests.", 500)
		}),
		Transport: &http.Transport{TLSClientConfig: tlsClientSkipVerify,
			Proxy: http.ProxyFromEnvironment},
		MITMCertAuth: GoproxyCa,
	}
	proxy.ConnectDial = dialerFromEnv(&proxy)
	return &proxy
}

// SetMITMCertAuth sets the CA to be used to sign man-in-the-middle'd
// certificates. You can load some []byte with `LoadCA()`. This bundle
// gets passed into the `ProxyCtx` and may be overridden in the [TODO:
// FIXME] `HandleConnect()` callback, before doing SNI sniffing.
func (proxy *ProxyHttpServer) SetMITMCertAuth(ca *tls.Certificate) {
	proxy.MITMCertAuth = ca
}
