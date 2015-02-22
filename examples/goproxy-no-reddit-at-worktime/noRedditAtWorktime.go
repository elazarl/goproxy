package main

import (
	"github.com/elazarl/goproxy"
	"log"
	"net/http"
	"time"
)

func main() {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest(goproxy.DstHostIs("www.reddit.com")).DoFunc(
		func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
			if h, _, _ := time.Now().Clock(); h >= 8 && h <= 17 {
				return r, goproxy.NewResponse(r,
					goproxy.ContentTypeText, http.StatusForbidden,
					"Don't waste your time!")
			}
			return r, nil
		})
	log.Fatalln(http.ListenAndServe(":8080", proxy))
}
