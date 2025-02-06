package main

import (
	"flag"
	"log"
	"net/http"
	"sync"

	"github.com/elazarl/goproxy"
)

func main() {
	verbose := flag.Bool("v", false, "should every proxy request be logged to stdout")
	addr := flag.String("addr", ":8080", "proxy listen address")
	flag.Parse()
	proxy := goproxy.NewProxyHttpServer()
	var mitmErrorHosts sync.Map
	proxy.OnRequest().HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		_, exists := mitmErrorHosts.Load(host)
		if exists {
			return goproxy.OkConnect, host
		}

		return &goproxy.ConnectAction{Action: goproxy.ConnectMitm, TLSConfig: goproxy.MitmConnect.TLSConfig, MitmError: func(req *http.Request, ctx *goproxy.ProxyCtx, err error) {
			log.Printf("Adding host to mitm error: %s", host)
			mitmErrorHosts.Store(host, true)
		}}, host
	})
	proxy.Verbose = *verbose
	log.Fatal(http.ListenAndServe(*addr, proxy))
}
