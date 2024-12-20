package main

import (
	"crypto/tls"
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

	proxyServer.OnRequest().HandleConnect(goproxy.FuncHttpsHandler(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		req := ctx.Req
		req.RequestURI = ""
		client := &http.Client{
			Transport: &http.Transport{
				Proxy: createSocksProxy(*socksAddr, auth),
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
		}
		var port string
		if strings.ContainsRune(host, ':') {
			host, port, _ = net.SplitHostPort(host)
		} else {
			port = "443"
		}
		switch port {
		case "80":
			req.URL.Scheme = "http"
		case "443":
			req.URL.Scheme = "https"
		default:
			ctx.Logf("Unsupported port: " + port)
			return nil, ""
		}

		ip, _ := net.ResolveIPAddr("ip", host)
		host = ip.String()
		host = net.JoinHostPort(host, port)
		req.URL.Host = host

		var err error
		ctx.Resp, err = client.Do(req)
		if err != nil {
			ctx.Logf("Failed to dial socks proxy: " + err.Error())
			return nil, ""
		}
		ctx.Logf("Succesfully dial to socks proxy")
		return &goproxy.ConnectAction{
			Action: goproxy.ConnectAccept,
		}, host
	}))

	proxyServer.Tr.Proxy = createSocksProxy(*socksAddr, auth)
	proxyServer.Verbose = *verbose

	log.Fatalln(http.ListenAndServe(*addr, proxyServer))
}
