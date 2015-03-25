package goproxy

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/inconshreveable/go-vhost"
)

// ProxyCtx is the Proxy context, contains useful information about every request. It is passed to
// every user function. Also used as a logger.
type ProxyCtx struct {
	Method        string
	SourceIP      string
	IsSecure      bool // Whether we are handling an HTTPS request with the client
	IsThroughMITM bool // Whether the current request is currently being MITM'd

	// Sniffed and non-sniffed hosts, cached here.
	host         string
	sniHost      string
	sniffedTLS   bool
	MITMCertAuth *tls.Certificate

	// OriginalRequest holds a copy of the request before doing some HTTP tunnelling through CONNECT, or doing a man-in-the-middle attack.
	OriginalRequest *http.Request

	// Will contain the client request from the proxy
	Req            *http.Request
	ResponseWriter http.ResponseWriter

	// Connections, up (the requester) and downstream (the server we forward to)
	Conn           net.Conn
	targetSiteConn net.Conn // used internally when we established a CONNECT session, to pass through new requests

	// Resp constains the remote sever's response (if available). This can be nil if the request wasn't sent yet, or if there was an error trying to fetch the response. In this case, refer to `ResponseError` for the latest error.
	Resp *http.Response

	// ResponseError contains the last error, if any, after running `ForwardRequest()` explicitly, or implicitly forwarding a request through other means (like returning `FORWARD` in some handlers).
	ResponseError error

	// originalResponseBody holds the first Response.Body (the original Response) in the chain.  This possibly exists if `Resp` is not nil.
	originalResponseBody io.ReadCloser

	RoundTripper RoundTripper

	// will contain the recent error that occured while trying to send receive or parse traffic
	Error error

	// A handle for the user to keep data in the context, from the call of ReqHandler to the
	// call of RespHandler
	UserData map[string]interface{}

	// Will connect a request to a response
	Session int64
	proxy   *ProxyHttpServer
}

// SNIHost will try preempt the TLS handshake and try to sniff the Server Name Indication.  This method will only sniff when handling a CONNECT request.
func (ctx *ProxyCtx) SNIHost() string {
	if ctx.Method != "CONNECT" {
		return ctx.Host()
	}

	if ctx.sniHost != "" {
		return ctx.sniHost
	}
	if _, ok := ctx.Conn.(vhost.TLSConn); ok {
		// We tried, and we didn't find SNI, fallback to Request
		return ctx.Host()
	}

	ctx.Conn.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))

	tlsConn, err := vhost.TLS(ctx.Conn)
	ctx.Conn = net.Conn(tlsConn)
	ctx.sniffedTLS = true
	if err != nil {
		ctx.Logf("Failed to sniff SNI (falling back to request Host): %s", err)
		return ctx.Host()
	}

	// TODO: make sure we put a ":port" on the `host` if there was one previously...
	ctx.sniHost = tlsConn.Host()
	ctx.host = ctx.sniHost
	return ctx.sniHost
}

// Host will return the host without sniffing the SNI extension in the TLS negotiation.  You should use `SNIHost()` if you want to support that. Using this method ensures unaltered behavior for CONNECT calls to remote TCP endpoints.
func (ctx *ProxyCtx) Host() string {
	return ctx.host
}

func (ctx *ProxyCtx) SetDestinationHost(host string) {
	ctx.Req.Host = host
	ctx.host = host
}

// CONNECT handling methods

func (ctx *ProxyCtx) ManInTheMiddle(host string) error {
	if strings.HasSuffix(host, ":80") || strings.IndexRune(host, ':') == -1 {
		return ctx.TunnelHTTP(host)
	} else {
		return ctx.ManInTheMiddleHTTPS(host)
	}
}

func (ctx *ProxyCtx) TunnelHTTP(host string) error {
	if !ctx.sniffedTLS {
		ctx.Conn.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))
	}

	ctx.Logf("Assuming CONNECT is plain HTTP tunneling, mitm proxying it")
	targetSiteConn, err := ctx.proxy.connectDial("tcp", host)
	if err != nil {
		ctx.Warnf("Error dialing to %s: %s", host, err.Error())
		return err
	}

	ctx.OriginalRequest = ctx.Req
	ctx.targetSiteConn = targetSiteConn
	ctx.RoundTripper = RoundTripperFunc(func(req *http.Request, ctx *ProxyCtx) (*http.Response, error) {
		remote := bufio.NewReader(ctx.targetSiteConn)
		resp := ctx.Resp
		if err := req.Write(ctx.targetSiteConn); err != nil {
			ctx.httpError(err)
			return nil, err
		}
		resp, err = http.ReadResponse(remote, req)
		if err != nil {
			ctx.httpError(err)
			return nil, err
		}
		return resp, nil
	})

	for {
		client := bufio.NewReader(ctx.Conn)
		req, err := http.ReadRequest(client)
		if err != nil && err != io.EOF {
			ctx.Warnf("cannot read request of MITM HTTP client: %+#v", err)
		}
		if err != nil {
			return err
		}

		ctx.Req = req
		ctx.IsSecure = false
		ctx.IsThroughMITM = true

		ctx.proxy.dispatchRequestHandlers(ctx)
	}

	return nil
}

func (ctx *ProxyCtx) ManInTheMiddleHTTPS(host string) error {
	ctx.Logf("Assuming CONNECT is TLS, mitm proxying it")

	tlsConfig, err := ctx.tlsConfig(host)
	if err != nil {
		ctx.Logf("Couldn't configure TLS: %s", err)
		ctx.httpError(err)
		return err
	}

	ctx.OriginalRequest = ctx.Req

	// this goes in a separate goroutine, so that the net/http server won't think we're
	// still handling the request even after hijacking the connection. Those HTTP CONNECT
	// request can take forever, and the server will be stuck when "closed".
	// TODO: Allow Server.Close() mechanism to shut down this connection as nicely as possible
	go func() {
		//TODO: cache connections to the remote website
		r := ctx.Req
		rawClientTls := tls.Server(ctx.Conn, tlsConfig)
		if err := rawClientTls.Handshake(); err != nil {
			ctx.Warnf("Cannot handshake client %v %v", r.Host, err)
			return
		}
		defer rawClientTls.Close()
		ctx.Conn = rawClientTls
		ctx.IsSecure = true

		clientTlsReader := bufio.NewReader(rawClientTls)
		for !isEof(clientTlsReader) {
			req, err := http.ReadRequest(clientTlsReader)
			if err != nil && err != io.EOF {
				return
			}
			if err != nil {
				ctx.Warnf("Cannot read TLS request from mitm'd client %v %v", r.Host, err)
				return
			}
			req.RemoteAddr = r.RemoteAddr // since we're converting the request, need to carry over the original connecting IP as well
			ctx.Logf("req %v", r.Host)
			req.URL, err = url.Parse("https://" + r.Host + req.URL.String())

			ctx.Req = req
			ctx.IsThroughMITM = true

			ctx.proxy.dispatchRequestHandlers(ctx)
		}
		ctx.Logf("Exiting on EOF")
	}()

	return nil
}

func (ctx *ProxyCtx) HijackConnect() net.Conn {
	if ctx.Method != "CONNECT" {
		panic("method is not CONNECT when HijackConnect() is called")
	}

	if !ctx.sniffedTLS {
		ctx.Conn.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))
	}

	return ctx.Conn
}

func (ctx *ProxyCtx) ForwardConnect(host string) error {
	if ctx.Method != "CONNECT" {
		return fmt.Errorf("Method is not CONNECT")
	}

	// TODO: fix up the port, if anyone changed it.. ensure we have a port.. or it matches the originally requested port (in the CONNECT call).
	if !hasPort.MatchString(host) {
		host += ":80"
	}
	targetSiteConn, err := ctx.proxy.connectDial("tcp", host)
	if err != nil {
		ctx.httpError(err)
		return err
	}

	if !ctx.sniffedTLS {
		ctx.Conn.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))
	}
	ctx.Logf("Accepting CONNECT to %s", host)
	go ctx.copyAndClose(targetSiteConn, ctx.Conn)
	go ctx.copyAndClose(ctx.Conn, targetSiteConn)
	return nil
}

var hasPort = regexp.MustCompile(`:\d+$`)

func (ctx *ProxyCtx) RejectConnect() {
	if ctx.Method != "CONNECT" {
		panic("cannot RejectConnect() when Method is not CONNECT")
	}

	// we had support here for flushing the Response when ctx.Resp was != nil.
	// this belongs to an upper layer, not down here.  Have your code do it instead.
	if !ctx.sniffedTLS {
		ctx.Conn.Write([]byte("HTTP/1.0 502 Rejected\r\n\r\n"))
	}

	ctx.Conn.Close()
}

// Request handling

func (ctx *ProxyCtx) ForwardRequest(host string) error {
	ctx.removeProxyHeaders()
	resp, err := ctx.RoundTrip(ctx.Req)
	ctx.Resp = resp
	if err != nil {
		ctx.ResponseError = err
		return err
	}
	ctx.originalResponseBody = resp.Body
	ctx.ResponseError = nil
	ctx.Logf("Received response %v", resp.Status)
	return nil
}

func (ctx *ProxyCtx) DispatchResponseHandlers() error {
	var then Next
	for _, handler := range ctx.proxy.responseHandlers {
		then = handler.Handle(ctx)

		switch then {
		case DONE:
			// TODO: ensure everything is properly shut down
			return nil
		case NEXT:
			continue
		case FORWARD:
			break
		case MITM:
			panic("MITM doesn't make sense when we are already parsing the request")
		case REJECT:
			panic("REJECT a response ? then do what, send a 500 back ?")
		default:
			panic(fmt.Sprintf("Invalid value %v for Next after calling %v", then, handler))
		}
	}

	if ctx.Resp == nil {
		err := fmt.Errorf("Response nil: %s", ctx.ResponseError)
		ctx.Logf("error read response %v %v:", ctx.Req.URL.Host, err.Error())
		http.Error(ctx.ResponseWriter, err.Error(), 500)
		return err
	}

	if ctx.IsThroughMITM && ctx.IsSecure {
		return ctx.ForwardMITMResponse(ctx.Resp)
	} else {
		return ctx.ForwardResponse(ctx.Resp)
	}
	return nil
}

func (ctx *ProxyCtx) ForwardResponse(resp *http.Response) error {
	w := ctx.ResponseWriter

	ctx.Logf("Copying response to client %v [%d]", resp.Status, resp.StatusCode)

	// http.ResponseWriter will take care of filling the correct response length
	// Setting it now, might impose wrong value, contradicting the actual new
	// body the user returned.
	// We keep the original body to remove the header only if things changed.
	// This will prevent problems with HEAD requests where there's no body, yet,
	// the Content-Length header should be set.
	if ctx.originalResponseBody != resp.Body {
		resp.Header.Del("Content-Length")
	}
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	nr, err := io.Copy(w, resp.Body)
	if err := resp.Body.Close(); err != nil {
		ctx.Warnf("Can't close response body %v", err)
	}
	ctx.Logf("Copied %d bytes to client, error=%v", nr, err)

	return nil
}

func (ctx *ProxyCtx) ForwardMITMResponse(resp *http.Response) error {
	// TODO: clarify this... why would we mangle the response with chunk encodings, but only
	// in the TLS MITM case ? isn't this arbitrary ?  Should we provide a user configurable
	// option to do so ?

	text := resp.Status
	statusCode := strconv.Itoa(resp.StatusCode) + " "
	if strings.HasPrefix(text, statusCode) {
		text = text[len(statusCode):]
	}
	// always use 1.1 to support chunked encoding
	if _, err := io.WriteString(ctx.Conn, "HTTP/1.1"+" "+statusCode+text+"\r\n"); err != nil {
		ctx.Warnf("Cannot write TLS response HTTP status from mitm'd client: %v", err)
		return err
	}
	// Since we don't know the length of resp, return chunked encoded response
	// TODO: use a more reasonable scheme
	resp.Header.Del("Content-Length")
	resp.Header.Set("Transfer-Encoding", "chunked")
	if err := resp.Header.Write(ctx.Conn); err != nil {
		ctx.Warnf("Cannot write TLS response header from mitm'd client: %v", err)
		return err
	}
	if _, err := io.WriteString(ctx.Conn, "\r\n"); err != nil {
		ctx.Warnf("Cannot write TLS response header end from mitm'd client: %v", err)
		return err
	}
	chunked := newChunkedWriter(ctx.Conn)
	if _, err := io.Copy(chunked, resp.Body); err != nil {
		ctx.Warnf("Cannot write TLS response body from mitm'd client: %v", err)
		return err
	}
	if err := chunked.Close(); err != nil {
		ctx.Warnf("Cannot write TLS chunked EOF from mitm'd client: %v", err)
		return err
	}
	if _, err := io.WriteString(ctx.Conn, "\r\n"); err != nil {
		ctx.Warnf("Cannot write TLS response chunked trailer from mitm'd client: %v", err)
		return err
	}

	return nil
}

func (ctx *ProxyCtx) tlsConfig(host string) (*tls.Config, error) {
	config := *defaultTLSConfig

	ca := ctx.proxy.MITMCertAuth
	if ctx.MITMCertAuth != nil {
		ca = ctx.MITMCertAuth
	}

	ctx.Logf("signing for %s", stripPort(host))
	cert, err := signHost(ca, []string{stripPort(host)})
	if err != nil {
		ctx.Warnf("Cannot sign host certificate with provided CA: %s", err)
		return nil, err
	}
	config.Certificates = append(config.Certificates, cert)
	return &config, nil
}

func (ctx *ProxyCtx) removeProxyHeaders() {
	r := ctx.Req
	r.RequestURI = "" // this must be reset when serving a request with the client
	ctx.Logf("Sending request %v %v", r.Method, r.URL.String())

	// If no Accept-Encoding header exists, Transport will add the headers it can accept
	// and would wrap the response body with the relevant reader.
	r.Header.Del("Accept-Encoding")

	// curl can add that, see
	// http://homepage.ntlworld.com/jonathan.deboynepollard/FGA/web-proxy-connection-header.html
	r.Header.Del("Proxy-Connection")

	// Connection is single hop Header:
	// http://www.w3.org/Protocols/rfc2616/rfc2616.txt
	// 14.10 Connection
	//   The Connection general-header field allows the sender to specify
	//   options that are desired for that particular connection and MUST NOT
	//   be communicated by proxies over further connections.
	r.Header.Del("Connection")
}

func (ctx *ProxyCtx) httpError(parentErr error) {
	ctx.Logf("Sending http error: %s", parentErr)

	if !ctx.sniffedTLS {
		if _, err := io.WriteString(ctx.Conn, "HTTP/1.1 502 Bad Gateway\r\n\r\n"); err != nil {
			ctx.Warnf("Error responding to client: %s", err)
		}
	}
	if err := ctx.Conn.Close(); err != nil {
		ctx.Warnf("Error closing client connection: %s", err)
	}
}

func (ctx *ProxyCtx) copyAndClose(w, r net.Conn) {
	connOk := true
	if _, err := io.Copy(w, r); err != nil {
		connOk = false
		ctx.Warnf("Error copying to client: %s", err)
	}
	if err := r.Close(); err != nil && connOk {
		ctx.Warnf("Error closing: %s", err)
	}
}

// Logf prints a message to the proxy's log. Should be used in a ProxyHttpServer's filter
// This message will be printed only if the Verbose field of the ProxyHttpServer is set to true
//
//	proxy.OnRequest().DoFunc(func(r *http.Request,ctx *goproxy.ProxyCtx) (*http.Request, *http.Response){
//		nr := atomic.AddInt32(&counter,1)
//		ctx.Printf("So far %d requests",nr)
//		return r, nil
//	})
func (ctx *ProxyCtx) Logf(msg string, argv ...interface{}) {
	if ctx.proxy.Verbose {
		ctx.printf("INFO: "+msg, argv...)
	}
}

// Warnf prints a message to the proxy's log. Should be used in a ProxyHttpServer's filter
// This message will always be printed.
//
//	proxy.OnRequest().DoFunc(func(r *http.Request,ctx *goproxy.ProxyCtx) (*http.Request, *http.Response){
//		f,err := os.OpenFile(cachedContent)
//		if err != nil {
//			ctx.Warnf("error open file %v: %v",cachedContent,err)
//			return r, nil
//		}
//		return r, nil
//	})
func (ctx *ProxyCtx) Warnf(msg string, argv ...interface{}) {
	ctx.printf("WARN: "+msg, argv...)
}

func (ctx *ProxyCtx) printf(msg string, argv ...interface{}) {
	ctx.proxy.Logger.Printf("[%03d] "+msg+"\n", append([]interface{}{ctx.Session & 0xFF}, argv...)...)
}

var charsetFinder = regexp.MustCompile("charset=([^ ;]*)")

// Will try to infer the character set of the request from the headers.
// Returns the empty string if we don't know which character set it used.
// Currently it will look for charset=<charset> in the Content-Type header of the request.
func (ctx *ProxyCtx) Charset() string {
	charsets := charsetFinder.FindStringSubmatch(ctx.Resp.Header.Get("Content-Type"))
	if charsets == nil {
		return ""
	}
	return charsets[1]
}
