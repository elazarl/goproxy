package auth

import (
	"bytes"
	"encoding/base64"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/elazarl/goproxy"
)

var unauthorizedMsg = []byte("407 Proxy Authentication Required")

func BasicUnauthorized(req *http.Request, realm string) *http.Response {
	// TODO(elazar): verify realm is well formed
	return &http.Response{
		StatusCode:    407,
		ProtoMajor:    1,
		ProtoMinor:    1,
		Request:       req,
		Header:        http.Header{"Proxy-Authenticate": []string{"Basic realm=" + realm}},
		Body:          ioutil.NopCloser(bytes.NewBuffer(unauthorizedMsg)),
		ContentLength: int64(len(unauthorizedMsg)),
	}
}

var proxyAuthorizationHeader = "Proxy-Authorization"

func auth(proxy *goproxy.ProxyHttpServer, ctx *goproxy.ProxyCtx, f func(user, passwd string) string) bool {
	authheader := strings.SplitN(ctx.Req.Header.Get(proxyAuthorizationHeader), " ", 2)
	ctx.Req.Header.Del(proxyAuthorizationHeader)
	if len(authheader) != 2 || authheader[0] != "Basic" {
		return false
	}
	userpassraw, err := base64.StdEncoding.DecodeString(authheader[1])
	if err != nil {
		return false
	}
	userpass := strings.SplitN(string(userpassraw), ":", 2)
	if len(userpass) != 2 {
		return false
	}

	// auth
	proxyURL := f(userpass[0], userpass[1])

	// invalid login
	if proxyURL == "" {
		return false
	}

	// set ctx username/password
	ctx.Username = userpass[0]
	ctx.Password = userpass[1]

	// set ctx proxy for http
	ctx.ProxyURL = proxyURL

	// set https proxy
	if proxy != nil {
		ctx.ConnectDial = proxy.NewConnectDialToProxy("http://" + proxyURL)
	}
	return true
}

// Basic returns a basic HTTP authentication handler for requests
//
// You probably want to use auth.ProxyBasic(proxy) to enable authentication for all proxy activities
func Basic(realm string, f func(user, passwd string) string) goproxy.ReqHandler {
	return goproxy.FuncReqHandler(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		if !auth(nil, ctx, f) {
			return nil, BasicUnauthorized(req, realm)
		}
		return req, nil
	})
}

// BasicConnect returns a basic HTTP authentication handler for CONNECT requests
//
// You probably want to use auth.ProxyBasic(proxy) to enable authentication for all proxy activities
func BasicConnect(proxy *goproxy.ProxyHttpServer, realm string, f func(user, passwd string) string) goproxy.HttpsHandler {
	return goproxy.FuncHttpsHandler(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		if !auth(proxy, ctx, f) {
			ctx.Resp = BasicUnauthorized(ctx.Req, realm)
			return goproxy.RejectConnect, host
		}
		return goproxy.OkConnect, host
	})
}

// ProxyBasic will force HTTP authentication before any request to the proxy is processed
func ProxyBasic(proxy *goproxy.ProxyHttpServer, realm string, f func(user, passwd string) string) {
	proxy.OnRequest().Do(Basic(realm, f))
	proxy.OnRequest().HandleConnect(BasicConnect(proxy, realm, f))
}
