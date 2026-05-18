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
	proxy.Verbose = *verboseLogging

	log.Printf("base proxy listening on %s", *listenAddr)
	log.Fatal(http.ListenAndServe(*listenAddr, proxy))
}
