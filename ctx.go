package goproxy

import (
	"context"
	"mime"
	"net"
	"net/http"
)

// ProxyCtx is the Proxy context, contains useful information about every request. It is passed to
// every user function. Also used as a logger.
type ProxyCtx struct {
	// Will contain the client request from the proxy
	Req *http.Request
	// Will contain the remote server's response (if available. nil if the request wasn't send yet)
	Resp         *http.Response
	Options      Options
	RoundTripper RoundTripper
	// Specify a custom connection dialer that will be used only for the current
	// request, including WebSocket connection upgrades
	Dialer func(ctx context.Context, network string, addr string) (net.Conn, error)
	// A handle for the user to keep data in the context, from the call of ReqHandler to the
	// call of RespHandler
	UserData any
	// Will connect a request to a response
	Session int64
	Proxy   *ProxyHttpServer
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
	return ctx.Proxy.opt.Transport.RoundTrip(req)
}

// Charset tries to infer the character set of the request, looking at the Content-Type header.
// This function returns an empty string if we are unable to determine which character set it used.
func (ctx *ProxyCtx) Charset() string {
	contentType := ctx.Resp.Header.Get("Content-Type")
	if _, params, err := mime.ParseMediaType(contentType); err == nil {
		if cs, ok := params["charset"]; ok {
			return cs
		}
	}
	return ""
}
