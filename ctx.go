package goproxy

import (
	"github.com/elazarl/goproxy/transport"
	"net/http"
	"regexp"
)

// ProxyCtx is the Proxy context, contains useful information about every request. It is passed to
// every user function. Also used as a logger.
type ProxyCtx struct {
	// Will contain the client request from the proxy
	Req *http.Request
	// Will contain the remote server's response (if available. nil if the request wasn't send yet)
	Resp  *http.Response
	RoundTrip *transport.RoundTripDetails
	// A handle for the user to keep data in the context, from the call of ReqHandler to the
	// call of RespHandler
	UserData interface{}
	sess  int32
	proxy *ProxyHttpServer
}

func (ctx *ProxyCtx) printf(msg string, argv ...interface{}) {
	ctx.proxy.Logger.Printf("[%03d] "+msg+"\n", append([]interface{}{ctx.sess & 0xFF}, argv...)...)
}

// Logf prints a message to the proxy's log. Should be used in a ProxyHttpServer's filter
// This message will be printed only if the Verbose field of the ProxyHttpServer is set to true
// 
//	proxy.OnRequest().DoFunc(func(r *http.Request,ctx *goproxy.ProxyCtx) *http.Request{
//		nr := atomic.AddInt32(&counter,1)
//		ctx.Printf("So far %d requests",nr)
//		return r
//	})
func (ctx *ProxyCtx) Logf(msg string, argv ...interface{}) {
	if ctx.proxy.Verbose {
		ctx.printf("INFO: "+msg, argv...)
	}
}

// Warnf prints a message to the proxy's log. Should be used in a ProxyHttpServer's filter
// This message will always be printed.
// 
//	proxy.OnRequest().DoFunc(func(r *http.Request,ctx *goproxy.ProxyCtx) *http.Request{
//		f,err := os.OpenFile(cachedContent)
//		if err != nil {
//			ctx.Warnf("error open file %v: %v",cachedContent,err)
//			return r
//		}
//		return r
//	})
func (ctx *ProxyCtx) Warnf(msg string, argv ...interface{}) {
	ctx.printf("WARN: "+msg, argv...)
}

var charsetFinder = regexp.MustCompile("charset=([^ ]*)")

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
