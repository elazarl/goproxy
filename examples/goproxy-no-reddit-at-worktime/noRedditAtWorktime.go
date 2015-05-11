package main

import (
	"github.com/abourget/goproxy"
	"log"
	"net/http"
	"time"
)

func main() {
	proxy := goproxy.NewProxyHttpServer()

	daytimeBlocker := goproxy.HandlerFunc(func(ctx *goproxy.ProxyCtx) goproxy.Next {
		if h, _, _ := time.Now().Clock(); h >= 8 && h <= 17 {
			ctx.NewResponse(http.StatusForbidden, "text/plain", "Don't waste your time!")
			return goproxy.FORWARD
		}
		return goproxy.NEXT
	})
	proxy.HandleRequest(goproxy.RequestHostIsIn("www.reddit.com")(daytimeBlocker))

	log.Fatalln(proxy.ListenAndServe(":8080"))
}
