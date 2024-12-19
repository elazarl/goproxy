// This example demonstrates how to configure goproxy to act as an HTTP/HTTPS proxy
// that forwards all traffic through a SOCKS5 proxy.
// The goproxy server acts as an aggregator, handling incoming HTTP and HTTPS requests
// and routing them via the SOCKS5 proxy.
// Example usage:
// socks proxy with no auth:
// 		go run socksproxy.go -v -addr ":8080" -socks "localhost:1080"
// socks with auth:
// 		go run socksproxy.go -v -addr ":8080" -socks "localhost:1080" -user "bob" -pass "123"

package main

import (
	"flag"
	"github.com/elazarl/goproxy"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
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
	proxyServer.Tr.Dial = func(network, addr string) (net.Conn, error) {
		return net.Dial(network, addr)
	}
	socksConnectDial, err := NewSocks5ConnectDialedToProxy(proxyServer, *socksAddr, &auth, func(req *http.Request) {
		if strings.ContainsRune(req.URL.Host, ':') {
			req.URL.Host = strings.Split(req.URL.Host, ":")[0]
		}
		resolvedAddr, _ := net.ResolveIPAddr("ip", req.URL.Host)
		req.URL.Host = resolvedAddr.String() + ":" + req.URL.Port()
	})
	if err != nil {
		log.Fatalf("failed to create SOCKS5 connect dial: %v", err)
	}
	proxyServer.ConnectDial = socksConnectDial
	proxyServer.Verbose = *verbose

	log.Fatalln(http.ListenAndServe(*addr, proxyServer))
}
