package main

import (
	"bytes"
	"crypto/tls"
	"flag"
	"io"
	"log"
	"net/http"
	"net/http/httptest"

	"github.com/elazarl/goproxy"
)

func main() {
	verboseLogging := flag.Bool("v", false, "log every proxy request to stdout")
	listenAddr := flag.String("addr", ":8080", "proxy listen address")
	flag.Parse()

	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "original data")
	}))
	backend.EnableHTTP2 = true
	backend.StartTLS()
	defer backend.Close()
	log.Printf("backend server listening on %s\n", backend.Listener.Addr().String())

	proxy := goproxy.NewProxyHttpServer()
	proxy.Verbose = *verboseLogging
	proxy.AllowHTTP2 = true
	proxy.Tr = &http.Transport{
		ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{"h2"},
		},
	}
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		resp.Body = io.NopCloser(bytes.NewReader([]byte("mitm response")))
		return resp
	})

	log.Printf("base proxy listening on %s\n", *listenAddr)
	log.Fatal(http.ListenAndServe(*listenAddr, proxy))
}
