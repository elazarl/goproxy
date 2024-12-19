package main

import (
	"flag"
	"github.com/elazarl/goproxy"
	"log"
	"net/http"
)

func main() {
	verbose := flag.Bool("v", false, "should every proxy request be logged to stdout")
	addr := flag.String("addr", ":8080", "proxy listen address")
	flag.Parse()

	proxy := goproxy.NewProxyHttpServer()
	proxy.CertStore = NewCertStorage()
	proxy.Verbose = *verbose

	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		// Log requested URL
		log.Println(req.URL.String())
		return req, nil
	})

	// Start proxy server
	log.Fatal(http.ListenAndServe(*addr, proxy))
}
