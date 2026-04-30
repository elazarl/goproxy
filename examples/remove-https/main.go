package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/elazarl/goproxy"
)

func main() {
	verboseLogging := flag.Bool("v", false, "log every proxy request to stdout")
	listenAddr := flag.String("addr", ":8080", "proxy listen address")
	flag.Parse()

	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		if req.URL.Scheme == "https" {
			req.URL.Scheme = "http"
		}
		return req, nil
	})
	proxy.Verbose = *verboseLogging

	log.Printf("remove-https proxy listening on %s", *listenAddr)
	log.Fatal(http.ListenAndServe(*listenAddr, proxy))
}
