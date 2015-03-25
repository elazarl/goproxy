package goproxy

type Next int

const (
	NEXT    = Next(iota) // Continue to the next Handler
	DONE                 // Implies that no further processing is required. The request has been fulfilled completely.
	FORWARD              // Continue directly with forwarding, going through Response Handlers
	MITM                 // Continue with Man-in-the-middle attempt, either through HTTP or HTTPS.
	REJECT               // Reject the CONNECT attempt outright
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
