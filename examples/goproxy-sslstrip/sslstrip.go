package main

import (
	"flag"
	"log"

	"github.com/abourget/goproxy"
)

func main() {
	verbose := flag.Bool("v", false, "should every proxy request be logged to stdout")
	addr := flag.String("addr", ":8080", "proxy listen address")
	flag.Parse()
	proxy := goproxy.NewProxyHttpServer()
	proxy.HandleConnect(goproxy.AlwaysMitm)
	proxy.HandleRequestFunc(func(ctx *goproxy.ProxyCtx) goproxy.Next {
		if ctx.Req.URL.Scheme == "https" {
			ctx.Req.URL.Scheme = "http"
		}
		return goproxy.NEXT
	})
	proxy.Verbose = *verbose
	log.Fatal(proxy.ListenAndServe(*addr))
}
