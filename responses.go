package goproxy

import (
	"bytes"
	"io/ioutil"
	"net/http"
)

// Will generate a valid http response to the given request the response will have
// the given contentType, and http status.
// Typical usage, refuse to process requests to local addresses:
//
//  proxy.HandleRequest(IsLocalhost(HandlerFunc(func(ctx *ProxyCtx) Next {
// 	    ctx.NewResponse(http.StatusUnauthorized, "text/html", "<html><body>Can't use proxy for local addresses</body></html>")
// 	    return FORWARD
//   })))
//
func NewResponse(r *http.Request, status int, contentType, body string) *http.Response {
	resp := &http.Response{}
	resp.Request = r
	resp.TransferEncoding = r.TransferEncoding
	resp.Header = make(http.Header)
	resp.Header.Add("Content-Type", contentType)
	resp.StatusCode = status
	buf := bytes.NewBufferString(body)
	resp.ContentLength = int64(buf.Len())
	resp.Body = ioutil.NopCloser(buf)
	return resp
}
