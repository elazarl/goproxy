package goproxy

type Next int

const (
	NEXT    = Next(iota) // Continue to the next Handler
	DONE                 // Implies that no further processing is required. The request has been fulfilled completely.
	FORWARD              // Continue directly with forwarding, going through Response Handlers
	MITM                 // Continue with Man-in-the-middle attempt, either through HTTP or HTTPS.
	REJECT               // Reject the CONNECT attempt outright
)

// About CONNECT requests

type Handler interface {
	Handle(ctx *ProxyCtx) Next
}

type HandlerFunc func(ctx *ProxyCtx) Next

func (f HandlerFunc) Handle(ctx *ProxyCtx) Next {
	return f(ctx)
}

type ChainedHandler func(Handler) Handler


var AlwaysMitm = HandlerFunc(func(ctx *ProxyCtx) Next {
	ctx.SNIHost()
	return MITM
})

var AlwaysReject = HandlerFunc(func(ctx *ProxyCtx) Next {
	return REJECT
})

var AlwaysForward = HandlerFunc(func(ctx *ProxyCtx) Next {
	return FORWARD
})
