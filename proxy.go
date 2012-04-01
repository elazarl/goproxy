package goproxy

import (
	"bufio"
	"io"
	"log"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sync/atomic"
)

// The basic proxy type. Implements http.Handler.
type ProxyHttpServer struct {
	// setting Verbose to true will log information on each request sent to the proxy
	Verbose       bool
	// 
	Logger        *log.Logger
	reqHandlers   []ReqHandler
	respHandlers  []RespHandler
	httpsHandlers []HttpsHandler
	sess          int32
	tr            *http.Transport
}

var hasPort = regexp.MustCompile(`:\d+$`)

func (proxy *ProxyHttpServer) copyAndClose(w io.WriteCloser, r io.Reader) {
	io.Copy(w, r)
	if err := w.Close(); err != nil {
		proxy.Logger.Println("Error closing", err)
	}
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
	_,err := r.Peek(1)
	if err == io.EOF {
		return true
	}
	return false
}


func (proxy *ProxyHttpServer) handleHttps(w http.ResponseWriter, r *http.Request) {
	hij, ok := w.(http.Hijacker)
	if !ok {
		panic("httpserver does not support hijacking")
	}

	host := r.URL.Host
	if !hasPort.MatchString(host) {
		host += ":80"
	}
	targetSiteCon, e := net.Dial("tcp", host)
	if e != nil {
		// trying to mimic the behaviour of the offending website
		// don't answer at all
		return
	}
	proxyClient, _, e := hij.Hijack()
	if e != nil {
		panic("Cannot hijack connection " + e.Error())
	}

	mitm := false
	for _, h := range proxy.httpsHandlers {
		if h.ShouldMitm(r.Host, r) {
			mitm = true
			break
		}
	}
	proxyClient.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))
	if mitm {
		// this goes in a separate goroutine, so that the net/http server won't think we're
		// still handling the request even after hijacking the connection. Those HTTP CONNECT
		// request can take forever, and the server will be stuck when "closed".
		// TODO: Allow Server.Close() mechanism to shut down this connection as nicely as possible
		go func() {
			ctx := &ProxyCtx{Req:r, sess:atomic.AddInt32(&proxy.sess,1), proxy: proxy}

			//TODO: cache connections to the remote website
			rawClientTls := tls.Server(proxyClient,tlsConfig)
			if err := rawClientTls.Handshake(); err != nil {
				ctx.Warnf("Cannot handshake client %v %v",r.Host,err)
			}
			defer rawClientTls.Close()
			clientTlsReader := bufio.NewReader(rawClientTls)
			for ! isEof(clientTlsReader) {
				req, err := http.ReadRequest(clientTlsReader)
				if err != nil {
					ctx.Warnf("Cannot read TLS request from mitm'd client %v %v",r.Host,err)
					return
				}
				ctx.Logf("req %v",r.Host)
				req, resp := proxy.filterRequest(req, ctx)
				if resp == nil {
					req.URL,err = url.Parse("https://"+r.Host+req.URL.Path)
					if err != nil {
						ctx.Warnf("Illegal URL %s","https://"+r.Host+req.URL.Path)
						return
					}
					resp,err = proxy.tr.RoundTrip(req)
					if err != nil {
						ctx.Warnf("Cannot read TLS response from mitm'd server %v",err)
						return
					}
					ctx.Logf("resp %v",resp.Status)
				}
				resp = proxy.filterResponse(resp, ctx)
				if err := resp.Write(rawClientTls); err != nil {
					ctx.Warnf("Cannot write TLS response from mitm'd client %v",err)
					return
				}
			}
			ctx.Logf("Exiting on EOF")
		}()
	} else {
		go proxy.copyAndClose(targetSiteCon, proxyClient)
		go proxy.copyAndClose(proxyClient, targetSiteCon)
	}
}
func (proxy *ProxyHttpServer) filterRequest(r *http.Request, ctx *ProxyCtx) (req *http.Request,resp *http.Response) {
	req = r
	for _, h := range proxy.reqHandlers {
		req,resp = h.Handle(r,ctx)
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

// Standard net/http function. Shouldn't be used directly, http.Serve will use it.
func (proxy *ProxyHttpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	//r.Header["X-Forwarded-For"] = w.RemoteAddr()
	if r.Method == "CONNECT" {
		proxy.handleHttps(w, r)
	} else {
		ctx := &ProxyCtx{Req:r, sess:atomic.AddInt32(&proxy.sess,1), proxy: proxy}

		var err error
		ctx.Logf("Got request %v %v",r.Method,r.URL.String())
		r,resp := proxy.filterRequest(r,ctx)

		if resp == nil {
			r.RequestURI = "" // this must be reset when serving a request with the client
			ctx.Logf("Sending request %v %v",r.Method,r.URL.String())
			// If no Accept-Encoding header exists, Transport will add the headers it can accept
			// and would wrap the response body with the relevant reader.
			r.Header.Del("Accept-Encoding")
			resp, err = proxy.tr.RoundTrip(r)
			if err != nil {
				ctx.Warnf("error read response %v %v:", r.URL.Host, err.Error())
				return
			}
			ctx.Logf("Recieved response %v",resp.Status)
		}
		resp = proxy.filterResponse(resp, ctx)

		// http.ResponseWriter will take care of filling the correct response length
		// Setting it now, might impose wrong value, contradicting the actual new
		// body the user returned.
		ctx.Logf("Copying response to client %v [%d]",resp.Status,resp.StatusCode)
		resp.Header.Del("Content-Length")
		copyHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		nr,err := io.Copy(w, resp.Body)
		if err := resp.Body.Close(); err != nil {
			ctx.Warnf("Can't close response body %v",err)
		}
		ctx.Logf("Copied %v bytes to client error=%v",nr,err)
	}
}

// New proxy server, logs to StdErr by default
func NewProxyHttpServer() *ProxyHttpServer {
	return &ProxyHttpServer{
		Logger:        log.New(os.Stderr, "", log.LstdFlags),
		reqHandlers:   []ReqHandler{},
		respHandlers:  []RespHandler{},
		httpsHandlers: []HttpsHandler{},
		tr:            &http.Transport{TLSClientConfig:tlsClientSkipVerify},
	}
}

