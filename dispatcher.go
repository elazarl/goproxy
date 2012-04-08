package goproxy

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"regexp"
	"strings"
)

// ReqCondition.HandleReq will decide whether or not to use the ReqHandler on an HTTP request
// before sending it to the remote server
type ReqCondition interface {
	HandleReq(req *http.Request, ctx *ProxyCtx) bool
}

// ReqCondition.HandleReq will decide whether or not to use the RespHandler on an HTTP response
// before sending it to the proxy client
type RespCondition interface {
	ReqCondition
	HandleResp(resp *http.Response, ctx *ProxyCtx) bool
}

// ReqConditionFunc.HandleReq(req,ctx) <=> ReqConditionFunc(req,ctx)
type ReqConditionFunc func(req *http.Request, ctx *ProxyCtx) bool

// RespConditionFunc.HandleResp(resp,ctx) <=> RespConditionFunc(resp,ctx)
type RespConditionFunc func(resp *http.Response, ctx *ProxyCtx) bool

func (c ReqConditionFunc) HandleReq(req *http.Request, ctx *ProxyCtx) bool {
	return c(req, ctx)
}

// RespConditionFunc cannot test requests. It only satisfies ReqCondition interface so that
// RespCondition and ReqCondition will be of the same type.
func (c RespConditionFunc) HandleReq(req *http.Request, ctx *ProxyCtx) bool {
	panic("RespCondition should never handle request, " +
		"it is of the same type just to have poor man's algebraid data types")
}

func (c RespConditionFunc) HandleResp(resp *http.Response, ctx *ProxyCtx) bool {
	return c(resp, ctx)
}

// Returns a ReqCondtion checking wether the destination URL the proxy client has requested
// has the given prefix, with or without the host.
// For example UrlHasPrefix("host/x") will match requests of the form 'GET host/x', and will match
// requests to url 'http://host/x'
func UrlHasPrefix(prefix string) ReqConditionFunc {
	return func(req *http.Request, ctx *ProxyCtx) bool {
		return strings.HasPrefix(req.URL.Path, prefix) ||
			strings.HasPrefix(req.URL.Host+"/"+req.URL.Path, prefix) ||
			strings.HasPrefix(req.URL.Scheme+req.URL.Host+req.URL.Path, prefix)
	}
}

// Returns a ReqCondition, testing whether or not the request URL is one of the given strings
// with or without the host prefix.
// UrlIs("google.com/","foo") will match requests 'GET /' to 'google.com', requests `'GET google.com/' to
// any host, and requests of the form 'GET foo'.
func UrlIs(urls ...string) ReqConditionFunc {
	urlSet := make(map[string]bool)
	for _,u := range urls {
		urlSet[u] = true
	}
	return func(req *http.Request, ctx *ProxyCtx) bool {
		_,pathOk    := urlSet[req.URL.Path]
		_,hostAndOk := urlSet[req.URL.Host+req.URL.Path]
		return pathOk || hostAndOk
	}
}

var localHostIpv4 = regexp.MustCompile(`127\.0\.0\.\d+`)
// Checks whether the destination host is explicitly local host (buggy, there can be IPv6 addresses it doesn't catch)
var IsLocalHost ReqConditionFunc = func(req *http.Request, ctx *ProxyCtx) bool {
	return req.URL.Host == "::1" ||
		req.URL.Host == "0:0:0:0:0:0:0:1" ||
		localHostIpv4.MatchString(req.URL.Host) ||
		req.URL.Host == "localhost"
}

// returns a ReqCondition testing whether the destination URL of the request matches the given regexp, with or without
// prefix
func UrlMatches(re *regexp.Regexp) ReqConditionFunc {
	return func(req *http.Request, ctx *ProxyCtx) bool {
		return re.MatchString(req.URL.Path) ||
			re.MatchString(req.URL.Host+req.URL.Path)
	}
}

// returns a ReqCondtion testing wether the host in the request url is the given string
func DstHostIs(host string) ReqConditionFunc {
	return func(req *http.Request, ctx *ProxyCtx) bool {
		return req.URL.Host == host
	}
}

// returns a ReqCondtion testing wether the source IP of the request is the given string
func SrcIpIs(ip string) ReqConditionFunc {
	return func(req *http.Request, ctx *ProxyCtx) bool {
		return strings.HasPrefix(req.RemoteAddr, ip+":")
	}
}

// returns a ReqCondtion negating the given ReqCondition
func Not(r ReqCondition) ReqConditionFunc {
	return func(req *http.Request, ctx *ProxyCtx) bool {
		return !r.HandleReq(req, ctx)
	}
}

// returns a RespCondition testing whether the HTTP response has Content-Type header equal
// to one of the given strings.
func ContentTypeIs(typ string, types ...string) RespConditionFunc {
	types = append(types, typ)
	return func(resp *http.Response, ctx *ProxyCtx) bool {
		contentType := resp.Header.Get("Content-Type")
		for _, typ := range types {
			if contentType == typ || strings.HasPrefix(contentType, typ+";") {
				return true
			}
		}
		return false
	}
}

// Will return a temporary ReqProxyConds struct, aggregating the given condtions.
// You will use the ReqProxyConds struct to register a ReqHandler, that would filter
// the request, only if all the given ReqCondition matched.
// Typical usage:
//	proxy.OnRequest(UrlIs("example.com/foo"),UrlMatches(regexp.MustParse(`.*\.exampl.\com\./.*`)).Do(...)
func (proxy *ProxyHttpServer) OnRequest(conds ...ReqCondition) *ReqProxyConds {
	return &ReqProxyConds{proxy, conds}
}

// aggregate ReqConditions for a ProxyHttpServer. Upon calling Do, it will register a ReqHandler that would
// handle the request if all conditions on the HTTP request are met.
type ReqProxyConds struct {
	proxy    *ProxyHttpServer
	reqConds []ReqCondition
}

// equivalent to proxy.OnRequest().Do(FuncReqHandler(f))
func (pcond *ReqProxyConds) DoFunc(f func(req *http.Request, ctx *ProxyCtx) (*http.Request,*http.Response)) {
	pcond.Do(FuncReqHandler(f))
}


// Will register the ReqHandler on the proxy, the ReqHandler will handle the HTTP request if all the conditions
// aggregated in the ReqProxyConds are met. Typical usage:
//	proxy.OnRequest().Do(handler) // will call handler.Handle(req,ctx) on every request to the proxy
//	proxy.OnRequest(cond1,cond2).Do(handler)
//	// given request to the proxy, will test if cond1.HandleReq(req,ctx) && cond2.HandleReq(req,ctx) are true
//	// if they are, will call handler.Handle(req,ctx)
func (pcond *ReqProxyConds) Do(h ReqHandler) {
	pcond.proxy.reqHandlers = append(pcond.proxy.reqHandlers,
		FuncReqHandler(func(r *http.Request, ctx *ProxyCtx) (*http.Request,*http.Response) {
			for _, cond := range pcond.reqConds {
				if !cond.HandleReq(r, ctx) {
					return r,nil
				}
			}
			return h.Handle(r,ctx)
		}))
}

// aggregate RespConditions for a ProxyHttpServer. Upon calling Do, it will register a RespHandler that would
// handle the HTTP response from remote server if all conditions on the HTTP response are met.
type ProxyConds struct {
	proxy    *ProxyHttpServer
	reqConds []ReqCondition
	respCond []RespCondition
}

// equivalent to proxy.OnResponse().Do(FuncRespHandler(f))
func (pcond *ProxyConds) DoFunc(f func(resp *http.Response, ctx *ProxyCtx) *http.Response) {
	pcond.Do(FuncRespHandler(f))
}

// Will register the RespHandler on the proxy, h.Handle(resp,ctx) will be called on every
// request that matches the conditions aggregated in pcond.
func (pcond *ProxyConds) Do(h RespHandler) {
	pcond.proxy.respHandlers = append(pcond.proxy.respHandlers,
		FuncRespHandler(func(resp *http.Response, ctx *ProxyCtx) *http.Response {
			for _, cond := range pcond.reqConds {
				if !cond.HandleReq(ctx.Req, ctx) {
					return resp
				}
			}
			for _, cond := range pcond.respCond {
				if !cond.HandleResp(resp, ctx) {
					return resp
				}
			}
			return h.Handle(resp, ctx)
		}))
}

// OnResponse is used when adding a response-filter to the HTTP proxy, usual pattern is
//	proxy.OnResponse(cond1,cond2).Do(handler) // handler.Handle(resp,ctx) will be used
//				// if cond1.HandleResp(resp) && cond2.HandleResp(resp)
func (proxy *ProxyHttpServer) OnResponse(conds ...ReqCondition) *ProxyConds {
	pconds := &ProxyConds{proxy, make([]ReqCondition, 0), make([]RespCondition, 0)}
	for _, cond := range conds {
		switch cond := cond.(type) {
		case RespCondition:
			pconds.respCond = append(pconds.respCond, cond)
		case ReqCondition:
			pconds.reqConds = append(pconds.reqConds, cond)
		}
	}
	return pconds
}

// MitmHost will cause the proxy server to eavesdrop an http connection when
// a client tries to CONNECT to a host name that matches any of the given regular expressions
func (proxy *ProxyHttpServer) MitmHostMatches(res... *regexp.Regexp) *ProxyHttpServer {
	proxy.httpsHandlers = append(proxy.httpsHandlers,FuncHttpsHandler(func(host string, ctx *ProxyCtx) *ConnectAction {
		for _, re := range res {
			if re.MatchString(host) {
				return MitmConnect
			}
		}
		return OkConnect
	}))
	return proxy
}

// MitmHost will cause the proxy server to eavesdrop an http connection when
// a client tries to CONNECT to any of the given hosts. Note, that you must
// append the port to the host name, so a typical host is twitter.com:443
func (proxy *ProxyHttpServer) MitmHost(hosts ...string) *ProxyHttpServer {
	// TODO(elazar): optimize on single host
	mitmHosts := make(map[string]bool)
	for _,host := range hosts {
		mitmHosts[host] = true
	}
	proxy.httpsHandlers = append(proxy.httpsHandlers,FuncHttpsHandler(func(host string, ctx *ProxyCtx) *ConnectAction {
		_,ok := mitmHosts[host]
		if ok {
			return MitmConnect
		}
		return OkConnect
	}))
	return proxy
}

// HandleBytes will return a RespHandler that read the entire body of the request
// to a byte array in memory, would run the user supplied f function on the byte arra,
// and will replace the body of the original response with the resulting byte array.
func HandleBytes(f func(b []byte, ctx *ProxyCtx)[]byte) RespHandler {
	return FuncRespHandler(func(resp *http.Response, ctx *ProxyCtx) *http.Response {
		b,err := ioutil.ReadAll(resp.Body)
		if err != nil {
			ctx.Warnf("Cannot read response %s",err)
			return resp
		}
		resp.Body.Close()

		resp.Body = ioutil.NopCloser(bytes.NewBuffer(f(b,ctx)))
		return resp
	})
}

