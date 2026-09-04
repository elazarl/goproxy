package goproxy

import (
	"bytes"
	"io"
	"net/http"
)

// Will generate a valid http response to the given request the response will have
// the given contentType, and http status.
// Typical usage, refuse to process requests to local addresses:
//
//	proxy.OnRequest(IsLocalHost()).DoFunc(func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request,*http.Response) {
//		return nil,NewResponse(r,goproxy.ContentTypeHtml,http.StatusUnauthorized,
//			`<!doctype html><html><head><title>Can't use proxy for local addresses</title></head><body/></html>`)
//	})
func NewResponse(r *http.Request, contentType string, status int, body string) *http.Response {
	resp := &http.Response{}
	resp.Request = r
	resp.TransferEncoding = r.TransferEncoding
	resp.Header = make(http.Header)
	resp.Header.Add("Content-Type", contentType)
	resp.StatusCode = status
	resp.Status = http.StatusText(status)
	resp.Proto = "HTTP/1.1"
	resp.ProtoMajor = 1
	resp.ProtoMinor = 1
	buf := bytes.NewBufferString(body)
	resp.ContentLength = int64(buf.Len())
	resp.Body = io.NopCloser(buf)
	return resp
}

const (
	// ContentTypeText is the MIME type for plain text responses.
	ContentTypeText = "text/plain"
	// ContentTypeHtml is the MIME type for HTML responses.
	ContentTypeHtml = "text/html"
)

// Alias for NewResponse(r,ContentTypeText,http.StatusAccepted,text).
func TextResponse(r *http.Request, text string) *http.Response {
	return NewResponse(r, ContentTypeText, http.StatusAccepted, text)
}

// responseBodyAllowed reports whether a response may carry a message body.
// RFC 9112 6.3: a response to HEAD, or with a 1xx, 204, or 304 status, ends at
// the first empty line "regardless of the header fields present", so framing one
// leaves those bytes to be misread as the next response.
//
// Keyed on status and method, not on the body value: an empty HTTP/1 body is
// http.NoBody, but the HTTP/2 transport uses its own unexported value, so a
// bodiless HTTP/2 response is not http.NoBody.
func responseBodyAllowed(req *http.Request, resp *http.Response) bool {
	if req != nil && req.Method == http.MethodHead {
		return false
	}
	switch {
	case resp.StatusCode >= 100 && resp.StatusCode <= 199:
		return false
	case resp.StatusCode == http.StatusNoContent, resp.StatusCode == http.StatusNotModified:
		return false
	default:
		return true
	}
}
