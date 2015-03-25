package goproxy

import (
	"fmt"
	"net/http"
)

// HandleConnectFunc and HandleConnect mimic the `net/http` handlers, and register handlers for CONNECT proxy calls.
func (proxy *ProxyHttpServer) HandleConnectFunc(f func(ctx *ProxyCtx) Next) {
	proxy.connectHandlers = append(proxy.connectHandlers, ConnectHandlerFunc(f))
}

func (proxy *ProxyHttpServer) HandleConnect(f ConnectHandler) {
	proxy.connectHandlers = append(proxy.connectHandlers, f)
}

// HandleRequestFunc and HandleRequest put hooks to handle certain requests. Note that MITM'd and HTTP requests that go through a CONNECT'd connection also go through those RequestHandlers.
func (proxy *ProxyHttpServer) HandleRequestFunc(f func(ctx *ProxyCtx) Next) {
	proxy.connectHandlers = append(proxy.connectHandlers, RequestHandlerFunc(f))
}

func (proxy *ProxyHttpServer) HandleRequest(f RequestHandler) {
	proxy.connectHandlers = append(proxy.connectHandlers, f)
}

// HandleResponseFunc and HandleResponse put hooks to handle certain requests. Note that MITM'd and HTTP requests that go through a CONNECT'd connection also go through those ResponseHandlers.
func (proxy *ProxyHttpServer) HandleResponseFunc(f func(ctx *ProxyCtx) Next) {
	proxy.connectHandlers = append(proxy.connectHandlers, ResponseHandlerFunc(f))
}

func (proxy *ProxyHttpServer) HandleResponse(f ResponseHandler) {
	proxy.connectHandlers = append(proxy.connectHandlers, f)
}

//////
////// dispatchers section //////
//////

func (proxy *ProxyHttpServer) dispatchConnectHandlers(ctx *ProxyCtx) {
	hij, ok := ctx.ResponseWriter.(http.Hijacker)
	if !ok {
		panic("httpserver does not support hijacking")
	}

	conn, _, err := hij.Hijack()
	if err != nil {
		panic("cannot hijack connection " + err.Error())
	}

	ctx.Conn = conn

	var then Next
	for _, handler := range proxy.connectHandlers {
		then = handler.Handle(ctx)
		switch then {
		case NEXT:
			continue

		case FORWARD:
			break

		case MITM:
			err := ctx.ManInTheMiddle(ctx.host)
			if err != nil {
				ctx.Logf("error MITM'ing: %s", err)
			}
			return

		case REJECT:
			ctx.RejectConnect()

		default:
			panic(fmt.Sprintf("Invalid value %v for Next after calling %v", then, handler))
		}
	}

	if err := ctx.ForwardConnect(ctx.host); err != nil {
		ctx.Logf("Failed forwarding in fallback clause: %s", err)
	}
}

func (proxy *ProxyHttpServer) dispatchRequestHandlers(ctx *ProxyCtx) {
	var then Next
	for _, handler := range proxy.requestHandlers {
		then = handler.Handle(ctx)
		switch then {
		case DONE:
			// TODO: ensure everything is properly shut down
			return
		case NEXT:
			continue
		case FORWARD:
			break
		case MITM:
			panic("MITM doesn't make sense when we are already parsing the request")
		case REJECT:
			ctx.ResponseWriter.WriteHeader(502)
			ctx.ResponseWriter.Write([]byte("Rejected by proxy"))
			return
		default:
			panic(fmt.Sprintf("Invalid value %v for Next after calling %v", then, handler))
		}
	}

	ctx.ForwardRequest(ctx.host)
	ctx.DispatchResponseHandlers()
}
