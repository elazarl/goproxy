package goproxy

import (
	"net/http"
)

type ProxyRequestHandler interface {
	AddRequestHandler(handler ReqHandler)
	AddResponseHandler(handler RespHandler)
	AddHTTPSHandler(handler HttpsHandler)
	ReqCondition

	HandleRequest(req *http.Request, ctx *ProxyCtx) (*http.Request, *http.Response)
	HandleResponse(resp *http.Response, ctx *ProxyCtx) *http.Response
	HandleConnect(req string, ctx *ProxyCtx) (*ConnectAction, string)
}

type defaultProxyRequestHandler struct{}

var _ ProxyRequestHandler = (*defaultProxyRequestHandler)(nil)

func newProxyRequestHandler() *defaultProxyRequestHandler {
	return &defaultProxyRequestHandler{}
}

func (h *defaultProxyRequestHandler) AddRequestHandler(handler ReqHandler) {}

func (h *defaultProxyRequestHandler) AddResponseHandler(handler RespHandler) {}

func (h *defaultProxyRequestHandler) AddHTTPSHandler(handler HttpsHandler) {}

func (h *defaultProxyRequestHandler) HandleRequest(req *http.Request, ctx *ProxyCtx) (*http.Request, *http.Response) {
	return req, nil
}

func (h *defaultProxyRequestHandler) HandleResponse(resp *http.Response, ctx *ProxyCtx) *http.Response {
	return nil
}

func (h *defaultProxyRequestHandler) HandleConnect(req string, ctx *ProxyCtx) (*ConnectAction, string) {
	return nil, ""
}
