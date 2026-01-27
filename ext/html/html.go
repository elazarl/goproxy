// extension to goproxy that will allow you to easily filter web browser related content.
package goproxy_html

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/yx-zero/goproxy-transparent"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/transform"
)

var IsHtml goproxy.RespCondition = goproxy.ContentTypeIs("text/html")

var IsCss goproxy.RespCondition = goproxy.ContentTypeIs("text/css")

var IsJavaScript goproxy.RespCondition = goproxy.ContentTypeIs("text/javascript",
	"application/javascript")

var IsJson goproxy.RespCondition = goproxy.ContentTypeIs("text/json")

var IsXml goproxy.RespCondition = goproxy.ContentTypeIs("text/xml")

var IsWebRelatedText goproxy.RespCondition = goproxy.ContentTypeIs(
	"text/html",
	"text/css",
	"text/javascript", "application/javascript",
	"text/xml",
	"text/json",
)

// HandleString will receive a function that filters a string, and will convert the
// request body to a utf8 string, according to the charset specified in the Content-Type
// header.
// guessing Html charset encoding from the <META> tags is not yet implemented.
func HandleString(f func(s string, ctx *goproxy.ProxyCtx) string) goproxy.RespHandler {
	return HandleStringReader(func(r io.Reader, ctx *goproxy.ProxyCtx) io.Reader {
		b, err := io.ReadAll(r)
		if err != nil {
			ctx.Warnf("Cannot read string from resp body: %v", err)
			return r
		}
		return bytes.NewBufferString(f(string(b), ctx))
	})
}

// Will receive an input stream which would convert the response to utf-8
// The given function must close the reader r, in order to close the response body.
func HandleStringReader(f func(r io.Reader, ctx *goproxy.ProxyCtx) io.Reader) goproxy.RespHandler {
	return goproxy.FuncRespHandler(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		if ctx.Error != nil {
			return nil
		}
		charsetName := ctx.Charset()
		if charsetName == "" {
			charsetName = "utf-8"
		}

		if strings.ToLower(charsetName) != "utf-8" {
			tr, _ := charset.Lookup(charsetName)
			if tr == nil {
				ctx.Warnf("Cannot convert from %s to utf-8: not found", charsetName)
				return resp
			}

			// Pass UTF-8 data to the callback f() function and convert its
			// result back to the original encoding
			r := transform.NewReader(resp.Body, tr.NewDecoder())
			newr := transform.NewReader(f(r, ctx), tr.NewEncoder())
			resp.Body = &readFirstCloseBoth{io.NopCloser(newr), resp.Body}
		} else {
			//no translation is needed, already at utf-8
			resp.Body = &readFirstCloseBoth{io.NopCloser(f(resp.Body, ctx)), resp.Body}
		}
		return resp
	})
}

type readFirstCloseBoth struct {
	r io.ReadCloser
	c io.Closer
}

func (rfcb *readFirstCloseBoth) Read(b []byte) (nr int, err error) {
	return rfcb.r.Read(b)
}
func (rfcb *readFirstCloseBoth) Close() error {
	err1 := rfcb.r.Close()
	err2 := rfcb.c.Close()
	if err1 != nil && err2 != nil {
		return errors.New(err1.Error() + ", " + err2.Error())
	}
	if err1 != nil {
		return err1
	}
	return err2
}
