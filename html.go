package goproxy

import ("net/http"
	"bytes"
	"io/ioutil"
	"regexp"
	"strings"
)

var IsHtml RespConditionFunc = ContentTypeIs("text/html")

var IsCss RespConditionFunc = ContentTypeIs("text/css")

var IsJavaScript RespConditionFunc = ContentTypeIs("text/javascript",
					"application/javascript")

var IsJson RespConditionFunc = ContentTypeIs("text/json")

var IsXml RespConditionFunc = ContentTypeIs("text/xml")

var IsWebRelatedText RespConditionFunc = ContentTypeIs("text/html",
					"text/css",
					"text/javascript","application/javascript",
					"text/xml",
					"text/json")

var charsetFinder = regexp.MustCompile("charset=([^ ]*)")

func (cond *ProxyConds) HandleBytes(f func(b []byte, ctx *ProxyCtx)[]byte) {
	cond.DoFunc(func(resp *http.Response, ctx *ProxyCtx) *http.Response {
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

func (ctx *ProxyCtx) Charset() string {
	charsets := charsetFinder.FindStringSubmatch(ctx.Req.Header.Get("Content-Type"))
	if charsets == nil {
		return ""
	}
	return charsets[0]
}

func (cond *ProxyConds) HandleUtf8String(f func(s string, ctx *ProxyCtx) string) {
	cond.HandleBytes(func (str []byte, ctx *ProxyCtx) []byte {
		charset := ctx.Charset()
		if charset != "" && strings.ToLower(charset) != "utf-8" {
			ctx.Warnf("HandleUtf8String ignoring bad encoding %s (%s)",charset,ctx.Resp.Header.Get("Content-Type"))
			return str
		}
		return []byte(f(string(str),ctx))
	})
}
