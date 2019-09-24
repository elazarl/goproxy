package goproxy

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ProxyCtx is the Proxy context, contains useful information about every request. It is passed to
// every user function. Also used as a logger.
type ProxyCtx struct {
	// Will contain the client request from the proxy
	Req *http.Request
	// Will contain the remote server's response (if available. nil if the request wasn't send yet)
	Resp         *http.Response
	RoundTripper RoundTripper
	// will contain the recent error that occurred while trying to send receive or parse traffic
	Error error
	// A handle for the user to keep data in the context, from the call of ReqHandler to the
	// call of RespHandler
	UserData interface{}
	// Will connect a request to a response
	Session   int64
	certStore CertStorage
	Proxy     *ProxyHttpServer

	ForwardProxy      string
	ForwardProxyAuth  string
	ForwardProxyProto string
	ProxyUser         string
	Accounting        string
	BytesSent         int64
	BytesReceived     int64
	Tail              func(*ProxyCtx) error
}

type RoundTripper interface {
	RoundTrip(req *http.Request, ctx *ProxyCtx) (*http.Response, error)
}

type CertStorage interface {
	Fetch(hostname string, gen func() (*tls.Certificate, error)) (*tls.Certificate, error)
}

type RoundTripperFunc func(req *http.Request, ctx *ProxyCtx) (*http.Response, error)

func (f RoundTripperFunc) RoundTrip(req *http.Request, ctx *ProxyCtx) (*http.Response, error) {
	return f(req, ctx)
}

func (ctx *ProxyCtx) RoundTrip(req *http.Request) (*http.Response, error) {
	if ctx.RoundTripper != nil {
		return ctx.RoundTripper.RoundTrip(req, ctx)
	}
	var tr *http.Transport
	d := net.Dialer{}

	host := req.URL.Host
	if !strings.Contains(req.URL.Host, ":") {
		host = req.URL.Host + ":80"
	}

	// Create our transport depending on behaviour (normal/proxied)
	var rawConn net.Conn
	var err error
	if ctx.ForwardProxy != "" {

		if ctx.ForwardProxyProto == "" {
			ctx.ForwardProxyProto = "http"
		}
		tr = &http.Transport{
			Proxy: func(req *http.Request) (*url.URL, error) {
				return url.Parse(ctx.ForwardProxyProto + "://" + ctx.ForwardProxy)
			},
			Dial: ctx.Proxy.NewConnectDialToProxyWithHandler(ctx.ForwardProxyProto+"://"+ctx.ForwardProxy, func(req *http.Request) {
				if ctx.ForwardProxyAuth != "" {
					req.Header.Set("Proxy-Authorization", fmt.Sprintf("Basic %s", ctx.ForwardProxyAuth))
				}
			}),
		}

		rawConn, err = tr.Dial("tcp4", host)
		if err != nil {
			return nil, err
		}
	} else {
		// Dial with regular transport
		tr = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			Dial:                  d.Dial,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}

		rawConn, err = tr.Dial("tcp4", host)
		if err != nil {
			return nil, err
		}
	}

	req.RequestURI = req.URL.String()

	conn := newProxyConn(rawConn)

	reader := bufio.NewReaderSize(conn, 32*1024)
	writer := bufio.NewWriterSize(conn, 32*1024)
	readDone := make(chan responseAndError, 1)
	writeDone := make(chan error, 1)

	// Write the request.
	go func() {
		var err error
		// Use writeproxy so as to not strip RequestURI if we
		// are forwarding to another proxy
		if ctx.ForwardProxy != "" {
			err = req.WriteProxy(writer)
		} else {
			err = req.Write(writer)
		}

		if err == nil {
			writer.Flush()
		}

		writeDone <- err
	}()

	// And read the response.
	go func() {
		resp, err := http.ReadResponse(reader, req)
		if err != nil {
			readDone <- responseAndError{nil, err}
			return
		}

		resp.Body = &connCloser{resp.Body, conn}

		readDone <- responseAndError{resp, nil}
	}()

	if err := <-writeDone; err != nil {
		return nil, err
	}

	ctx.BytesSent = conn.BytesWrote

	r := <-readDone
	if r.err != nil {
		return nil, r.err
	}

	return r.resp, nil
}

func (ctx *ProxyCtx) printf(msg string, argv ...interface{}) {
	ctx.Proxy.Logger.Printf("[%03d] "+msg+"\n", append([]interface{}{ctx.Session & 0xFF}, argv...)...)
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
	if ctx.Proxy.Verbose {
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
