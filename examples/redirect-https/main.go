package main

import (
	"bytes"
	"flag"
	"io"
	"log"
	"net/http"

	"github.com/elazarl/goproxy"
)

func main() {
	verboseLogging := flag.Bool("v", false, "log every proxy request to stdout")
	listenAddr := flag.String("addr", ":8080", "proxy listen address")
	flag.Parse()

	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		if req.URL.Scheme != "https" {
			return req, nil
		}

		req.URL.Scheme = "http"
		resp := &http.Response{
			StatusCode: http.StatusSeeOther,
			ProtoMajor: 1,
			ProtoMinor: 1,
			Request:    req,
			Header: http.Header{
				"Location": []string{req.URL.String()},
			},
			Body:          io.NopCloser(bytes.NewReader(nil)),
			ContentLength: 0,
		}
		return nil, resp
	})
	proxy.Verbose = *verboseLogging

	log.Printf("redirect-https proxy listening on %s", *listenAddr)
	log.Fatal(http.ListenAndServe(*listenAddr, proxy))
}
