package auth

import (
	"bytes"
	"encoding/base64"
	"github.com/elazarl/goproxy"
	"io/ioutil"
	"net/http"
	"strings"
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

var proxyAuthorizatonHeader = "Proxy-Authorization"

func Basic(realm string, f func(user, passwd string) bool) goproxy.ReqHandler {
	return goproxy.FuncReqHandler(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		authheader := strings.SplitN(req.Header.Get(proxyAuthorizatonHeader), " ", 2)
		req.Header.Del(proxyAuthorizatonHeader)
		if len(authheader) != 2 || authheader[0] != "Basic" {
			return nil, BasicUnauthorized(req, realm)
		}
		userpassraw, err := base64.StdEncoding.DecodeString(authheader[1])
		if err != nil {
			return nil, BasicUnauthorized(req, realm)
		}
		userpass := strings.SplitN(string(userpassraw), ":", 2)
		if len(userpass) != 2 {
			return nil, BasicUnauthorized(req, realm)
		}
		if !f(userpass[0], userpass[1]) {
			return nil, BasicUnauthorized(req, realm)
		}
		return req, nil
	})
}
