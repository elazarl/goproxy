package goproxy

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// The basic proxy type. Implements http.Handler.
type ProxyHttpServer struct {
	sess                   int64 // session variable must be aligned in i386
	KeepDestinationHeaders bool  // retain all headers in http.Response before proxying
	Verbose                bool  // if true, logs information on each request
	Logger                 *log.Logger
	NonproxyHandler        http.Handler
	reqHandlers            []ReqHandler
	respHandlers           []RespHandler
	httpsHandlers          []HttpsHandler
	Tr                     *http.Transport
	// ConnectDial will be used to create TCP connections for CONNECT requests
	// if nil Tr.Dial will be used
	ConnectDial func(network string, addr string) (net.Conn, error)
	// Signer can be set by consumers with their own implementation.  This allows
	// f.e. for caching of Certificates.
	Signer   func(ca *tls.Certificate, hostname []string) (*tls.Certificate, error)
	WsServer *websocket.Upgrader
	WsDialer *websocket.Dialer
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

// Hop-by-hop headers. These are removed when sent to the backend.
// http://www.w3.org/Protocols/rfc2616/rfc2616-sec13.html
var hopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Connection",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

func writeResponse(ctx *ProxyCtx, resp *http.Response, out http.ResponseWriter) {
	ctx.Logf("Copying response to client: %v (%d bytes)", resp.Status, resp.ContentLength)

	// Fancy ResponseWriter
	if w, ok := out.(*connResponseWriter); ok {
		// net/http: Response.Write produces invalid responses in this case,
		// hacking to fix that
		if resp.ContentLength == -1 {
			defer resp.Body.Close()

			peek, err := ioutil.ReadAll(
				io.LimitReader(resp.Body, 4*1024),
			)

			body := bytes.NewReader(peek)

			if err != nil {
				ctx.Warnf("Error reading response body: %s", err.Error())
			}

			if len(peek) < 4*1024 {
				resp.ContentLength = int64(body.Len())
				resp.Body = ioutil.NopCloser(body)
			} else {
				resp.TransferEncoding = append(resp.TransferEncoding, "chunked")
				resp.Body = ioutil.NopCloser(io.MultiReader(
					body,
					resp.Body,
				))
			}
		}

		if err := resp.Write(w); err != nil {
			ctx.Warnf("Error writing response: %s", err.Error())
		} else {
			ctx.Logf("Copied response to client")
		}

		return
	}

	// Standard ResponseWriter
	// 1
	for k, _ := range out.Header() {
		out.Header().Del(k)
	}

	for k, vs := range resp.Header {
		for _, v := range vs {
			out.Header().Add(k, v)
		}
	}

	// 2
	out.WriteHeader(resp.StatusCode)

	// 3
	if nr, err := io.Copy(out, resp.Body); err != nil {
		ctx.Logf("Copied %v bytes to client with error: %v", nr, err)
	} else {
		ctx.Logf("Copied %v bytes to client", nr)
	}

	// 4
	if err := resp.Body.Close(); err != nil {
		ctx.Warnf("Can't close response body: %v", err)
	}
}

// Standard net/http function. Shouldn't be used directly, http.Serve will use it.
func (proxy *ProxyHttpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "CONNECT" {
		// Can be SSL and WebSockets
		proxy.handleConnect(w, r)
	} else {
		// Common HTTP proxy
		proxy.handleRequest(w, r, nil)
	}
}

func (proxy *ProxyHttpServer) handleRequest(writer http.ResponseWriter, base *http.Request, ctx *ProxyCtx) (bool, error) {
	if ctx == nil {
		ctx = &ProxyCtx{
			Req:       base,
			Session:   atomic.AddInt64(&proxy.sess, 1),
			Websocket: websocket.IsWebSocketUpgrade(base),
			proxy:     proxy,
		}
	}

	// Clean-up
	base.RequestURI = ""

	if !base.URL.IsAbs() {
		proxy.NonproxyHandler.ServeHTTP(writer, base)
		return true, nil
	}

	if websocket.IsWebSocketUpgrade(base) {
		return proxy.handleWsRequest(ctx, writer, base)
	} else {
		return proxy.handleHttpRequest(ctx, writer, base)
	}
}

func (proxy *ProxyHttpServer) handleHttpRequest(ctx *ProxyCtx, writer http.ResponseWriter, base *http.Request) (bool, error) {
	var (
		req  *http.Request
		resp *http.Response
		err  error
	)

	ctx.Logf("Relying http(s) request to: %v", base.URL.String())

	req, resp = proxy.filterRequest(base, ctx)
	if resp == nil {
		// Clean-up request
		for _, h := range hopHeaders {
			req.Header.Del(h)
		}

		// Sent the request
		resp, err = ctx.RoundTrip(req)
	}

	if err != nil {
		ctx.Logf("Error reading response %v: %v", req.URL.Host, err.Error())

		if operr, ok := err.(*net.OpError); ok && operr.Timeout() {
			Error(writer, fmt.Errorf("[proxy] timeout during request to remote server: %v", err), http.StatusGatewayTimeout)
		} else {
			Error(writer, fmt.Errorf("[proxy] error during request to remote server: %v", err), http.StatusBadGateway)
		}

		// We put the error into the ctx and allow the filters to do
		// their job on an empty response.  The ctx might contain
		// other relevant information that is going to be processed.
		ctx.Error = err
		go proxy.filterResponse(nil, ctx)
		return true, err
	}

	body := resp.Body
	defer body.Close()

	resp = proxy.filterResponse(resp, ctx)

	// http.ResponseWriter will take care of filling the correct response length
	// Setting it now, might impose wrong value, contradicting the actual new
	// body the user returned.
	// We keep the original body to remove the header only if things changed.
	// This will prevent problems with HEAD requests where there's no body, yet,
	// the Content-Length header should be set.
	if body != resp.Body {
		resp.Header.Del("Content-Length")
	}

	ctx.Logf("Received response: %v", resp.Status)

	writeResponse(ctx, resp, writer)

	return false, err
}

// TODO: add handshake filter and message introspection
func (proxy *ProxyHttpServer) handleWsRequest(ctx *ProxyCtx, writer http.ResponseWriter, base *http.Request) (bool, error) {
	proto := websocket.Subprotocols(base)

	switch base.URL.Scheme {
	case "http":
		base.URL.Scheme = "ws"

	case "https":
		base.URL.Scheme = "wss"
	}

	var resp *http.Response
	base, resp = proxy.filterRequest(base, ctx)
	if resp != nil {
		// a filter matched, let's return to the client
		writeResponse(ctx, resp, writer)
		return true, nil
	}

	ctx.Logf("Relying websocket connection %s with protocols: %v", base.URL.String(), proto)

	// TODO: The header needs to be configurable.
	header := http.Header(map[string][]string{})
	for k, v := range base.Header {
		if k == "Upgrade" || k == "Connection" || strings.HasPrefix(strings.ToLower(k), "sec-websocket") {
			continue
		}
		header[k] = v
	}
	header.Set("Origin", "http://proxy/")
	header["Sec-WebSocket-Protocol"] = proto

	remote, resp, err := proxy.WsDialer.Dial(base.URL.String(), header)

	if err != nil {
		ctx.Warnf("error ws-dialing %v: %v, resp: %v", base.URL, err, resp)
		if body, err := ioutil.ReadAll(resp.Body); err == nil {
			ctx.Warnf("ws-error body: %s", string(body))
		}
		if err == websocket.ErrBadHandshake {
			writeResponse(ctx, resp, writer)
		} else {
			Error(writer, err, http.StatusBadGateway)
		}

		// We put the error into the ctx and allow the filters to do
		// their job on an empty response.  The ctx might contain
		// other relevant information that is going to be processed.
		ctx.Error = err
		go proxy.filterResponse(nil, ctx)
		return true, err
	}
	resp = proxy.filterResponse(resp, ctx)

	client, err := proxy.WsServer.Upgrade(writer, base, header)
	if err != nil {
		return true, err
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go wsRelay(ctx, remote, client, &wg)
	go wsRelay(ctx, client, remote, &wg)
	wg.Wait()

	remote.Close()
	client.Close()

	return true, nil
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
		Tr: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
				DualStack: true,
			}).DialContext,
			MaxIdleConns:          100,
			IdleConnTimeout:       10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			TLSClientConfig:       tlsClientSkipVerify,
			DisableCompression:    true,
		},
		WsDialer: &websocket.Dialer{TLSClientConfig: tlsClientSkipVerify},
		WsServer: &websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
			Error: func(w http.ResponseWriter, r *http.Request, status int, reason error) {
				Error(w, reason, status)
			},
		},
	}
	proxy.ConnectDial = dialerFromEnv(&proxy)
	return &proxy
}
