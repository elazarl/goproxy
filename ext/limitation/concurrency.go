package limitation

import (
	"net/http"

	"github.com/yx-zero/goproxy-transparent"
)

// ConcurrentRequests implements a mechanism to limit the number of
// concurrently handled HTTP requests, configurable by the user.
// The ReqHandler can simply be added to the server with OnRequest().
func ConcurrentRequests(limit int) goproxy.ReqHandler {
	// Do nothing when the specified limit is invalid
	if limit <= 0 {
		return goproxy.FuncReqHandler(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
			return req, nil
		})
	}

	limitation := make(chan struct{}, limit)
	return goproxy.FuncReqHandler(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		limitation <- struct{}{}

		// Release semaphore when request finishes
		go func() {
			<-req.Context().Done()
			<-limitation
		}()

		return req, nil
	})
}
