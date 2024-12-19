package main

import (
	"flag"
	"github.com/elazarl/goproxy"
	"log"
	"net"
	"net/http"
	"net/url"
)

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

	proxyServer.OnRequest().Do(goproxy.FuncReqHandler(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		resolvedAddr, _ := net.ResolveIPAddr("ip", req.URL.Host)
		req.URL.Host = resolvedAddr.String() + ":" + req.URL.Port()
		return req, nil
	}))
	proxyServer.Tr.Proxy = createSocksProxy(*socksAddr, auth)
	socksConnectDial, err := NewSocks5ConnectDialedToProxy(proxyServer, *socksAddr, &auth, nil)
	if err != nil {
		log.Fatalf("failed to create SOCKS5 connect dial: %v", err)
	}
	proxyServer.ConnectDial = socksConnectDial
	proxyServer.Verbose = *verbose

	log.Fatalln(http.ListenAndServe(*addr, proxyServer))
}
