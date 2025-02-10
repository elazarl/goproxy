package main

import (
	"crypto/tls"
	"flag"
	"log"
	"net/http"
	"net/url"

	"github.com/elazarl/goproxy"
)

type SocksAuth struct {
	Username, Password string
}

func createSocksProxy(socksAddr string, auth SocksAuth) func(r *http.Request) (*url.URL, error) {
	return func(r *http.Request) (*url.URL, error) {
		Url := &url.URL{
			Scheme: "socks5",
			Host:   socksAddr,
		}
		if auth.Username != "" {
			Url.User = url.UserPassword(auth.Username, auth.Password)
		}
		return Url, nil
	}
}

func main() {
	verbose := flag.Bool("v", false, "should every proxy request be logged to stdout")
	addr := flag.String("addr", ":8080", "proxy listen address")
	socksAddr := flag.String("socks", "127.0.0.1:1080", "socks proxy address")
	username := flag.String("user", "", "username for SOCKS5 proxy if auth is required")
	password := flag.String("pass", "", "password for SOCKS5 proxy")
	flag.Parse()

	auth := SocksAuth{
		Username: *username,
		Password: *password,
	}
	proxyServer := goproxy.NewProxyHttpServer()

	proxyServer.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		client := &http.Client{
			Transport: &http.Transport{
				Proxy: createSocksProxy(*socksAddr, auth),
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
		}

		// https://stackoverflow.com/questions/19595860/http-request-requesturi-field-when-making-request-in-go
		req.RequestURI = ""

		resp, err := client.Do(req)
		if err != nil {
			ctx.Logf("Failed to forward request: " + err.Error())
			return nil, nil
		}
		ctx.Logf("Succesfully forwarded request to socks proxy")
		return req, resp
	})

	proxyServer.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxyServer.Verbose = *verbose

	log.Fatalln(http.ListenAndServe(*addr, proxyServer))
}
