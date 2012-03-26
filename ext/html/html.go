// extension to goproxy that will allow you to easily filter web browser related content.
package goproxy_html

import ("github.com/elazarl/goproxy"
	"net/http"
	"bytes"
	"io/ioutil"
	"code.google.com/p/go-charset/charset"
)

var IsHtml goproxy.RespConditionFunc = goproxy.ContentTypeIs("text/html")

var IsCss goproxy.RespConditionFunc = goproxy.ContentTypeIs("text/css")

var IsJavaScript goproxy.RespConditionFunc = goproxy.ContentTypeIs("text/javascript",
					"application/javascript")

var IsJson goproxy.RespConditionFunc = goproxy.ContentTypeIs("text/json")

var IsXml goproxy.RespConditionFunc = goproxy.ContentTypeIs("text/xml")

var IsWebRelatedText goproxy.RespConditionFunc = goproxy.ContentTypeIs("text/html",
					"text/css",
					"text/javascript","application/javascript",
					"text/xml",
					"text/json")

// HandleString will recieve a function that filters a string, and will convert the
// request body to a utf8 string, according to the charset specified in the Content-Type
// header.
// guessing Html charset encoding from the <META> tags is not yet implemented.
func HandleString(f func(s string, ctx *goproxy.ProxyCtx) string) goproxy.RespHandler {
	return goproxy.FuncRespHandler(func (resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		r,err := charset.NewReader(ctx.Charset(),resp.Body)
		if err != nil {
			ctx.Warnf("Cannot convert from %v to utf: %v",ctx.Charset(),err)
			return resp
		}
		defer resp.Body.Close()
		b,err := ioutil.ReadAll(r)
		if err != nil {
			ctx.Warnf("Cannot read string from resp body: %v",err)
			return resp
		}
		resp.Body = ioutil.NopCloser(bytes.NewBufferString(f(string(b),ctx)))
		return resp
	})
}
