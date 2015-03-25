package match

import (
	"bytes"
	"io/ioutil"
	"net/http"
)

// HandleBytes will return a RespHandler that read the entire body of the request
// to a byte array in memory, would run the user supplied f function on the byte arra,
// and will replace the body of the original response with the resulting byte array.
func HandleBytes(f func(b []byte, ctx *ProxyCtx) []byte) RespHandler {
	return RespHandlerFunc(func(resp *http.Response, ctx *ProxyCtx) *http.Response {
		b, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			ctx.Warnf("Cannot read response %s", err)
			return resp
		}
		resp.Body.Close()

		resp.Body = ioutil.NopCloser(bytes.NewBuffer(f(b, ctx)))
		return resp
	})
}
