package main

import (
	"bufio"
	"flag"
	"log"
	"net"
	"net/http"
	"regexp"

	"github.com/yx-zero/goproxy-transparent"
)

func main() {
	proxy := goproxy.NewProxyHttpServer()
	// Reject all requests to baidu
	proxy.OnRequest(goproxy.ReqHostMatches(regexp.MustCompile("baidu.*:443$"))).
		HandleConnect(goproxy.AlwaysReject)

	// Instead of returning the Internet response, send custom data from
	// our proxy server, using connection hijack
	proxy.OnRequest(goproxy.ReqHostMatches(regexp.MustCompile("^.*$"))).
		HijackConnect(func(req *http.Request, client net.Conn, ctx *goproxy.ProxyCtx) {
			client.Write([]byte("HTTP/1.1 200 Ok\r\n\r\n"))

			w := bufio.NewWriter(client)

			resp := &http.Response{
				StatusCode: http.StatusOK,
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header: http.Header{
					"test": {"1234"},
				},
			}
			resp.Write(w)
			w.Flush()
			client.Close()
		})

	verbose := flag.Bool("v", false, "should every proxy request be logged to stdout")
	addr := flag.String("addr", ":8080", "proxy listen address")
	flag.Parse()
	proxy.Verbose = *verbose
	log.Fatal(http.ListenAndServe(*addr, proxy))
}
