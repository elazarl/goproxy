package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/abourget/goproxy"
)

func main() {
	verbose := flag.Bool("v", false, "should every proxy request be logged to stdout")
	addr := flag.String("addr", ":8080", "proxy listen address")
	flag.Parse()
	proxy := goproxy.NewProxyHttpServer()
	proxy.Verbose = *verbose

	// test with: curl -v --proxy http://127.0.0.1:8080 -k https://google.com/

	proxy.HandleConnectFunc(func(ctx *goproxy.ProxyCtx) goproxy.Next {
		if ctx.SNIHost() == "google.com" {
			ctx.SetDestinationHost("www.bing.com:443")
			// so that Bing receives the right `Host:` header
			ctx.Req.Host = "www.bing.com"
		}

		return goproxy.MITM
	})
	proxy.HandleRequestFunc(func(ctx *goproxy.ProxyCtx) goproxy.Next {
		// When doing MITM, if we've rewritten the destination host, let,s sync the
		// `Host:` header so the remote endpoints answers properly.
		if ctx.IsThroughMITM {
			ctx.Req.Host = ctx.Host()
			return goproxy.FORWARD // don't follow through other Request Handlers
		}
		return goproxy.NEXT
	})

	// test with: curl -v --proxy http://127.0.0.1:8080 -k https://example.com/

	proxy.HandleRequestFunc(func(ctx *goproxy.ProxyCtx) goproxy.Next {
		if ctx.Host() == "example.com" {
			ctx.Req.Host = "www.cheezburger.com"
			ctx.Req.URL.Host = "www.cheezburger.com"
			//ctx.SetDestinationHost("www.cheezburger.com:80")
			return goproxy.FORWARD
		}
		return goproxy.NEXT
	})

	proxy.NonProxyHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		w.Write([]byte("hello world!\n"))
	})

	log.Println("Listening", *addr)
	log.Fatal(proxy.ListenAndServe(*addr))
}
