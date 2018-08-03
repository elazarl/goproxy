package goproxy

import (
	"fmt"
	"net/http"
	"regexp"

	"github.com/sirupsen/logrus"
)

// ProxyCtx is the Proxy context, contains useful information about every request. It is passed to
// every user function. Also used as a logger.
type ProxyCtx struct {
	logrus.Fields

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

	TargHost  string
	ProxyHost string

	// Will connect a request to a response
	Session int64
	proxy   *ProxyHttpServer
}

type RoundTripper interface {
	RoundTrip(req *http.Request, ctx *ProxyCtx) (*http.Response, error)
}

type RoundTripperFunc func(req *http.Request, ctx *ProxyCtx) (*http.Response, error)

func (f RoundTripperFunc) RoundTrip(req *http.Request, ctx *ProxyCtx) (*http.Response, error) {
	return f(req, ctx)
}

func (ctx *ProxyCtx) RoundTrip(req *http.Request) (*http.Response, error) {
	if ctx.RoundTripper != nil {
		return ctx.RoundTripper.RoundTrip(req, ctx)
	}
	return ctx.proxy.Tr.RoundTrip(req)
}

func (ctx *ProxyCtx) Printf(format string, args ...interface{}) {
	// ctx.proxy.Logger.Printf(format, append([]interface{}{ctx.Session & 0xFF}, args...)...)
	ctx.proxy.Logger.Info(fmt.Sprintf(format, args...))
}

func (ctx *ProxyCtx) Errorf(format string, args ...interface{}) {
	errmsg := fmt.Errorf(format, args...)
	if ctx.Error == nil {
		ctx.Error = errmsg
	}
	ctx.proxy.Logger.Error(errmsg)
}

func (ctx *ProxyCtx) Warnf(format string, args ...interface{}) {
	ctx.proxy.Logger.Warn(fmt.Sprintf(format, args...))
}

func (ctx *ProxyCtx) StatsField(ns string, value map[string]interface{}) logrus.Fields {
	if len(ns) == 0 || len(value) == 0 {
		return logrus.Fields{}
	}
	ctx.Fields = logrus.Fields{
		"stats": map[string]interface{}{
			ns: value,
		},
	}
	return ctx.Fields
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
