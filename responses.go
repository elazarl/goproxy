package goproxy

import ("net/http"
	"io/ioutil"
	"bytes")

func NewResponse(r *http.Request, contentType, body string,status int) *http.Response {
	resp := &http.Response{}
	resp.Request = r
	resp.TransferEncoding = r.TransferEncoding
	resp.Header = make(http.Header)
	resp.Header.Add("Content-Type",contentType)
	resp.StatusCode = status
	buf := bytes.NewBufferString(body)
	resp.ContentLength = int64(buf.Len())
	resp.Body = ioutil.NopCloser(buf)
	return resp
}

func TextResponse(r *http.Request, text string) *http.Response {
	return NewResponse(r,"text/plain",text,200)
}

func NotFoundTextResponse(r *http.Request, text string) *http.Response {
	return NewResponse(r,"text/plain",text,404)
}

func ForbiddenTextResponse(r *http.Request, text string) *http.Response {
	return NewResponse(r,"text/plain",text,403)
}
