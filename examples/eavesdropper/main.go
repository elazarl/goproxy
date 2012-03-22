package main

import (
	"github.com/elazarl/goproxy"
	"log"
	"regexp"
	"net/http"
)

func main() {
	proxy := goproxy.NewProxyHttpServer()
	proxy.MitmHostMatches(regexp.MustCompile("^.*$"))
	proxy.Verbose = true
	log.Fatal(http.ListenAndServe(":8080", proxy))
}
