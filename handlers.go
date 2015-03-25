package goproxy

import "net/http"

type Next int

const (
	NEXT    = Next(iota) // Continue to the next Handler
	DONE    // Implies that no further processing is required. The request has been fulfilled completely.
	FORWARD // Continue directly with forwarding, going through Response Handlers
	MITM    // Continue with Man-in-the-middle attempt, either through HTTP or HTTPS.
	REJECT  // Reject the CONNECT attempt outright
)

type ConnectHandler interface {
	Handle(ctx *ProxyCtx) Next
}

type ConnectHandlerFunc func(ctx *ProxyCtx) Next

func (f ConnectHandlerFunc) Handle(ctx *ProxyCtx) Next {
	return f(ctx)
}

type RequestHandler interface {
	Handle(ctx *ProxyCtx) Next
}

type RequestHandlerFunc func(ctx *ProxyCtx) Next

func (f RequestHandlerFunc) Handle(ctx *ProxyCtx) Next {
	return f(ctx)
}

type ResponseHandler interface {
	Handle(ctx *ProxyCtx) Next
}

type ResponseHandlerFunc func(ctx *ProxyCtx) Next

func (f ResponseHandlerFunc) Handle(ctx *ProxyCtx) Next {
	return f(ctx)
}

// ReqHandler will "tamper" with the request coming to the proxy server
// If Handle returns req,nil the proxy will send the returned request
// to the destination server. If it returns nil,resp the proxy will
// skip sending any requests, and will simply return the response `resp`
// to the client.
type ReqHandler interface {
	Handle(req *http.Request, ctx *ProxyCtx) (*http.Request, *http.Response)
}

// A wrapper that would convert a function to a ReqHandler interface type
type ReqHandlerFunc func(req *http.Request, ctx *ProxyCtx) (*http.Request, *http.Response)

// ReqHandlerFunc.Handle(req,ctx) <=> FuncReqHandler(req,ctx)
func (f ReqHandlerFunc) Handle(req *http.Request, ctx *ProxyCtx) (*http.Request, *http.Response) {
	return f(req, ctx)
}

// after the proxy have sent the request to the destination server, it will
// "filter" the response through the RespHandlers it has.
// The proxy server will send to the client the response returned by the RespHandler.
// In case of error, resp will be nil, and ctx.RoundTrip.Error will contain the error
type RespHandler interface {
	Handle(resp *http.Response, ctx *ProxyCtx) *http.Response
}

// A wrapper that would convert a function to a RespHandler interface type
type RespHandlerFunc func(resp *http.Response, ctx *ProxyCtx) *http.Response

// RespHandlerFunc.Handle(req,ctx) <=> FuncRespHandler(req,ctx)
func (f RespHandlerFunc) Handle(resp *http.Response, ctx *ProxyCtx) *http.Response {
	return f(resp, ctx)
}

// When a client send a CONNECT request to a host, the request is filtered through
// all the HttpsHandlers the proxy has, and if one returns true, the connection is
// sniffed using Man in the Middle attack.
// That is, the proxy will create a TLS connection with the client, another TLS
// connection with the destination the client wished to connect to, and would
// send back and forth all messages from the server to the client and vice versa.
// The request and responses sent in this Man In the Middle channel are filtered
// through the usual flow (request and response filtered through the ReqHandlers
// and RespHandlers)
type HttpsHandler interface {
	HandleConnect(req string, ctx *ProxyCtx) (*ConnectAction, string)
}

// A wrapper that would convert a function to a HttpsHandler interface type
type HttpsHandlerFunc func(host string, ctx *ProxyCtx) (*ConnectAction, string)

// HttpsHandlerFunc should implement the RespHandler interface
func (f HttpsHandlerFunc) HandleConnect(host string, ctx *ProxyCtx) (*ConnectAction, string) {
	return f(host, ctx)
}
