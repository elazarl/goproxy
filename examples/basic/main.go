package main

import (
	"github.com/elazarl/goproxy"
	"log"
	"flag"
	"net/http"
)

func main() {
	verbose := flag.Bool("v", false, "should every proxy request be logged to stdout")
	flag.Parse()
	proxy := goproxy.NewProxyHttpServer()
	proxy.Verbose = *verbose
	log.Fatal(http.ListenAndServe(":8080", proxy))
}
