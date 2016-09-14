package main

import (
	"github.com/elazarl/goproxy"
	"log"
	"net/http"
	"regexp"
)

func main() {
	// Init
	https := regexp.MustCompile("^.*:(443|8443)$")

	proxy := goproxy.NewProxyHttpServer()
	proxy.Verbose = true

	// MitM
	proxy.OnRequest().HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		if ctx.Req.Header.Get("Connection") == "Upgrade" {
			return goproxy.RejectConnect, host
		}

		if https.MatchString(host) {
			return goproxy.MitmConnect, host
		} else {
			return goproxy.HTTPMitmConnect, host
		}
	})

	log.Fatal(http.ListenAndServe(":8888", proxy))
}
