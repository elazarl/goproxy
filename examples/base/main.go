package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/elazarl/goproxy"
)

func main() {
	verbose := flag.Bool("v", false, "should every proxy request be logged to stdout")
	addr := flag.String("addr", ":8080", "proxy listen address")
	flag.Parse()
	opt := goproxy.DefaultOptions()
	if *verbose {
		opt.Logger = goproxy.NewDefaultLogger(goproxy.DEBUG)
	}
	proxy := goproxy.NewProxyHttpServer(opt)
	log.Fatal(http.ListenAndServe(*addr, proxy))
}
