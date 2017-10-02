package main

import (
	"github.com/elazarl/goproxy"
	"log"
	"net/http"
	"regexp"
)

func main() {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest(goproxy.ReqHostMatches(regexp.MustCompile("^.*$"))).
		HandleConnect(goproxy.AlwaysMitm)
	proxy.Verbose = true
	log.Fatal(http.ListenAndServe(":8080", proxy))
}
