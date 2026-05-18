package main

import (
	"log"
	"net/http"
	"time"

	"github.com/elazarl/goproxy"
)

const (
	workStartHour = 8
	workEndHour   = 17
)

func main() {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest(goproxy.DstHostIs("www.reddit.com")).DoFunc(
		func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
			currentHour, _, _ := time.Now().Clock()
			if currentHour >= workStartHour && currentHour <= workEndHour {
				return r, goproxy.NewResponse(r,
					goproxy.ContentTypeText, http.StatusForbidden,
					"Don't waste your time!")
			}
			ctx.Warnf("clock: %d, you can waste your time...", currentHour)
			return r, nil
		})
	log.Println("request-filtering proxy listening on :8080")
	log.Fatalln(http.ListenAndServe(":8080", proxy))
}
