package goproxy

import (
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"sync/atomic"
	"github.com/gorilla/websocket"
	"sync"
	"io/ioutil"
	"bytes"
)

// The basic proxy type. Implements http.Handler.
type ProxyHttpServer struct {
	// session variable must be aligned in i386
	// see http://golang.org/src/pkg/sync/atomic/doc.go#L41
	sess            int64
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
	ConnectDial     func(network string, addr string) (net.Conn, error)
	WsServer        *websocket.Upgrader
	WsDialer        *websocket.Dialer
}

var hasPort = regexp.MustCompile(`:\d+$`)

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
				io.LimitReader(resp.Body, 4 * 1024),
			)

			body := bytes.NewReader(peek)

			if err != nil {
				ctx.Warnf("Error copying response: %s", err.Error())
			}

			if len(peek) < 4 * 1024 {
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
			ctx.Warnf("Error copying response: %s", err.Error())
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
		// CONNECT requests: SSL and WebSockets
		proxy.handleConnect(w, r)
	} else if !r.URL.IsAbs() {
		// Local requests
		proxy.NonproxyHandler.ServeHTTP(w, r)
	} else {
		// Common HTTP proxy
		proxy.handleRequest(w, r)
	}
}

func (proxy *ProxyHttpServer) handleRequest(writer http.ResponseWriter, base *http.Request) (bool, error) {
	ctx := &ProxyCtx{
		Req: base,
		Session: atomic.AddInt64(&proxy.sess, 1),
		Websocket: websocket.IsWebSocketUpgrade(base),
		proxy: proxy,
	}

	if websocket.IsWebSocketUpgrade(base) {
		return proxy.handleWsRequest(ctx, writer, base)
	} else {
		return proxy.handleHttpRequest(ctx, writer, base)
	}
}

// TODO add handshake filter and message introspection
func (proxy *ProxyHttpServer) handleWsRequest(ctx *ProxyCtx, writer http.ResponseWriter, base *http.Request) (bool, error) {
	proto := websocket.Subprotocols(base)
	wg := &sync.WaitGroup{}

	switch base.URL.Scheme {
	case "http":
		base.URL.Scheme = "ws"

	case "https":
		base.URL.Scheme = "wss"
	}

	ctx.Logf("Relying websocket connection %s with protocols: %v",  base.URL.String(), proto)

	remote, resp, err := proxy.WsDialer.Dial(
		base.URL.String(),
		nil,
	)

	if err != nil {
		if err == websocket.ErrBadHandshake {
			writeResponse(ctx, resp, writer)
		} else {
			http.Error(writer, err.Error(), http.StatusBadGateway)
		}

		return true, err
	}

	client, err := proxy.WsServer.Upgrade(writer, base, nil)

	if err != nil {
		return true, err
	}

	wg.Add(2)

	go wsRelay(ctx, remote, client, wg)
	go wsRelay(ctx, client, remote, wg)

	wg.Wait()

	remote.Close()
	client.Close()

	return true, nil
}

func (proxy *ProxyHttpServer) handleHttpRequest(ctx *ProxyCtx, writer http.ResponseWriter, base *http.Request) (bool, error) {
	var (
		req *http.Request
		resp *http.Response
		err error
	)

	ctx.Logf("Relying http(s) request to: %v", base.URL.String())

	req, resp = proxy.filterRequest(base, ctx)

	if resp == nil {
		removeProxyHeaders(ctx, req)
		resp, err = ctx.RoundTrip(req)
	}

	if err != nil {
		ctx.Logf("Error reading response %v: %v", req.URL.Host, err.Error())

		// TODO: add gateway timeout error in case of timeout
		switch err {
		default:
			http.Error(writer, err.Error(), http.StatusBadGateway)
		}

		return false, err
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

// New proxy server, logs to StdErr by default
func NewProxyHttpServer() *ProxyHttpServer {
	proxy := ProxyHttpServer{
		Logger:        log.New(os.Stderr, "", log.LstdFlags),
		reqHandlers:   []ReqHandler{},
		respHandlers:  []RespHandler{},
		httpsHandlers: []HttpsHandler{},
		NonproxyHandler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			http.Error(w, "This is a proxy server. Does not respond to non-proxy requests.", 500)
		}),
		Tr: &http.Transport{TLSClientConfig: tlsClientSkipVerify,
			Proxy: http.ProxyFromEnvironment},

		WsDialer: &websocket.Dialer{},
		WsServer: &websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}
	proxy.ConnectDial = dialerFromEnv(&proxy)
	return &proxy
}
