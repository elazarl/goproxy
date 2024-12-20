package main

import (
	"crypto/tls"
	"flag"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"

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
		var host, port string
		if strings.ContainsRune(req.URL.Host, ':') {
			splited := strings.Split(req.URL.Host, ":")
			host = splited[0]
			port = splited[1]
		} else {
			host = req.URL.Host
			port = "80"
		}

		req.RequestURI = ""
		switch port {
		case "80":
			req.URL.Scheme = "http"
		case "443":
			req.URL.Scheme = "https"
		default:
			ctx.Logf("Unsupported port: " + port)
			return nil, nil
		}

		ip, err := net.ResolveIPAddr("ip", host)
		if err != nil {
			ctx.Logf("Failed to resolve host: " + err.Error())
			return nil, nil
		}
		host = net.JoinHostPort(ip.String(), port)
		req.URL.Host = host
		req.Host = host
		resp, err := client.Do(req)
		if err != nil {
			ctx.Logf("Failed to forward request: " + err.Error())
			return nil, nil
		}
		ctx.Logf("Succesfully dial to socks proxy")
		return req, resp
	})

	proxyServer.OnRequest().HandleConnect(goproxy.AlwaysMitm)

	proxyServer.Tr.Proxy = createSocksProxy(*socksAddr, auth)
	proxyServer.Verbose = *verbose

	log.Fatalln(http.ListenAndServe(*addr, proxyServer))
}
