// extension to goproxy that adds support for filtering or handling MP4 video content.
package goproxy_mp4

import (
	"io"
	"net/http"

	"github.com/elazarl/goproxy"
)

// IsMp4Video is a response condition matching typical Content-Type headers for MP4 videos.
var IsMp4Video goproxy.RespCondition = goproxy.ContentTypeIs(
	"video/mp4",
	"application/mp4",
)

// HandleMp4Stream takes a function to process or wrap an MP4 stream (io.Reader) and returns a goproxy.RespHandler.
// The function can modify, inspect, or simply proxy the stream as desired.
func HandleMp4Stream(f func(r io.Reader, ctx *goproxy.ProxyCtx) io.Reader) goproxy.RespHandler {
	return goproxy.FuncRespHandler(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		// Replace the response body with our handler, ensuring we close both bodies.
		resp.Body = &readFirstCloseBoth{io.NopCloser(f(resp.Body, ctx)), resp.Body}
		return resp
	})
}

// readFirstCloseBoth wraps two io.Closers and closes both when Close is called.
// This is required to ensure the proxy and the handler both get cleaned up.
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
		return err1 // return the first error if both non-nil (could combine if needed)
	}
	if err1 != nil {
		return err1
	}
	return err2
}