package main

import (
	"flag"
	"github.com/elazarl/goproxy"
	"log"
	"net/http"
	"net/url"
)

func main() {
	verbose := flag.Bool("v", false, "should every proxy request be logged to stdout")
	addr := flag.String("addr", ":8080", "proxy listen address")
	upstream := flag.String("upstream", "", "upstream proxy if relevant")
	flag.Parse()
	proxy := goproxy.NewProxyHttpServer()
	proxy.Verbose = *verbose

	log.Printf("Listening on %s", *addr)

	if (*upstream != "") {
		log.Printf("Setting upstream proxy to %s", *upstream)
		proxy.Tr = &http.Transport{Proxy: func(req *http.Request) (*url.URL, error) {
			return url.Parse(*upstream)
		}}
		proxy.ConnectDial = proxy.NewConnectDialToProxy(*upstream)

	}
	log.Fatal(http.ListenAndServe(*addr, proxy))

}
