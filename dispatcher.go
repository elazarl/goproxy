package goproxy

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"regexp"
	"strings"
)

type ReqCondition interface {
	HandleReq(req *http.Request) bool
}

type RespCondition interface {
	ReqCondition
	HandleResp(resp *http.Response, req *http.Request) bool
}

type ReqConditionFunc func(req *http.Request) bool

type RespConditionFunc func(resp *http.Response, req *http.Request) bool

func (c ReqConditionFunc) HandleReq(req *http.Request) bool {
	return c(req)
}

func (c RespConditionFunc) HandleReq(req *http.Request) bool {
	panic("RespCondition should never handle request, " +
		"it is of the same type just to have poor man's algebraid data types")
}

func (c RespConditionFunc) HandleResp(resp *http.Response, req *http.Request) bool {
	return c(resp, req)
}

func UrlHasPrefix(prefix string) ReqConditionFunc {
	return func(req *http.Request) bool {
		return strings.HasPrefix(req.URL.Path, prefix) ||
			strings.HasPrefix(req.URL.Host+"/"+req.URL.Path, prefix) ||
			strings.HasPrefix(req.URL.Scheme+req.URL.Host+req.URL.Path, prefix)
	}
}

func UrlIs(urls ...string) ReqConditionFunc {
	urlSet := make(map[string]bool)
	for _,u := range urls {
		urlSet[u] = true
	}
	return func(req *http.Request) bool {
		_,pathOk    := urlSet[req.URL.Path]
		_,hostAndOk := urlSet[req.URL.Host+req.URL.Path]
		return pathOk || hostAndOk
	}
}

var localHostIpv4 = regexp.MustCompile(`127\.0\.0\.\d`)
var IsLocalHost ReqConditionFunc = func(req *http.Request) bool {
	return req.URL.Host == "::1" ||
		req.URL.Host == "0:0:0:0:0:0:0:1" ||
		localHostIpv4.MatchString(req.URL.Host) ||
		req.URL.Host == "localhost"
}

func UrlMatches(re *regexp.Regexp) ReqConditionFunc {
	return func(req *http.Request) bool {
		return re.MatchString(req.URL.Path) ||
			re.MatchString(req.URL.Host+req.URL.Path)
	}
}

func DstHostIs(host string) ReqConditionFunc {
	return func(req *http.Request) bool {
		return req.URL.Host == host
	}
}

func SrcIpIs(ip string) ReqConditionFunc {
	return func(req *http.Request) bool {
		return strings.HasPrefix(req.RemoteAddr, ip+":")
	}
}

func Not(r ReqCondition) ReqConditionFunc {
	return func(req *http.Request) bool {
		return !r.HandleReq(req)
	}
}

func ContentTypeIs(typ string, types ...string) RespConditionFunc {
	types = append(types, typ)
	return func(resp *http.Response, _ *http.Request) bool {
		contentType := resp.Header.Get("Content-Type")
		for _, typ := range types {
			if contentType == typ || strings.HasPrefix(contentType, typ+";") {
				return true
			}
		}
		return false
	}
}

func (proxy *ProxyHttpServer) OnRequest(conds ...ReqCondition) *ReqProxyConds {
	return &ReqProxyConds{proxy, conds}
}

type ReqProxyConds struct {
	proxy    *ProxyHttpServer
	reqConds []ReqCondition
}

func (pcond *ReqProxyConds) DoFunc(f func(req *http.Request, ctx *ProxyCtx) (*http.Request,*http.Response)) {
	pcond.Do(FuncReqHandler(f))
}

func (pcond *ReqProxyConds) Do(h ReqHandler) {
	pcond.proxy.reqHandlers = append(pcond.proxy.reqHandlers,
		FuncReqHandler(func(r *http.Request, ctx *ProxyCtx) (*http.Request,*http.Response) {
			for _, cond := range pcond.reqConds {
				if !cond.HandleReq(r) {
					return r,nil
				}
			}
			return h.Handle(r,ctx)
		}))
}

type ProxyConds struct {
	proxy    *ProxyHttpServer
	reqConds []ReqCondition
	respCond []RespCondition
}

func (pcond *ProxyConds) DoFunc(f func(resp *http.Response, ctx *ProxyCtx) *http.Response) {
	pcond.Do(FuncRespHandler(f))
}

func (pcond *ProxyConds) Do(h RespHandler) {
	pcond.proxy.respHandlers = append(pcond.proxy.respHandlers,
		FuncRespHandler(func(resp *http.Response, ctx *ProxyCtx) *http.Response {
			for _, cond := range pcond.reqConds {
				if !cond.HandleReq(ctx.Req) {
					return resp
				}
			}
			for _, cond := range pcond.respCond {
				if !cond.HandleResp(resp, ctx.Req) {
					return resp
				}
			}
			return h.Handle(resp, ctx)
		}))
}

// OnResponse is used when adding a response-filter to the HTTP proxy, usual pattern is
//    proxy.OnResponse(...conditions when filter applies...).Do(filter)
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
	proxy.httpsHandlers = append(proxy.httpsHandlers,FuncHttpsHandler(func(host string,_ *http.Request) bool {
		for _, re := range res {
			if re.MatchString(host) {
				return true
			}
		}
		return false
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
	proxy.httpsHandlers = append(proxy.httpsHandlers,FuncHttpsHandler(func(host string,_ *http.Request) bool {
		_,ok := mitmHosts[host]
		return ok
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

