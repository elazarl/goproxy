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
	"context"
	"flag"
	"fmt"
	"github.com/elazarl/goproxy"
	"golang.org/x/net/proxy"
	"log"
	"net"
	"net/http"
)

func createSocksDialer(socksAddr string, auth proxy.Auth) func(network, addr string) (net.Conn, error) {
	return func(network, addr string) (net.Conn, error) {
		dialer, err := proxy.SOCKS5(network, socksAddr, &auth, proxy.Direct)
		if err != nil {
			return nil, err
		}
		resolvedAddr, err := resolveAddress(addr)
		if err != nil {
			return nil, err
		}
		return dialer.Dial(network, resolvedAddr)
	}
}

func createSocksDialerContext(socksAddr string, auth proxy.Auth) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialer, err := proxy.SOCKS5(network, socksAddr, &auth, proxy.Direct)
		if err != nil {
			return nil, err
		}
		resolvedAddr, err := resolveAddress(addr)
		if err != nil {
			return nil, err
		}
		return dialer.Dial(network, resolvedAddr)
	}
}

func resolveAddress(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("invalid address format: %w", err)
	}

	ipAddr, err := net.ResolveIPAddr("ip", host)
	if err != nil {
		return "", fmt.Errorf("failed to resolve hostname: %w", err)
	}

	return net.JoinHostPort(ipAddr.String(), port), nil
}
func main() {
	verbose := flag.Bool("v", false, "should every proxy request be logged to stdout")
	addr := flag.String("addr", ":8080", "proxy listen address")
	socksAddr := flag.String("socks", "127.0.0.1:1080", "socks proxy address")
	username := flag.String("user", "", "username for SOCKS5 proxy")
	password := flag.String("pass", "", "password for SOCKS5 proxy")
	flag.Parse()

	auth := proxy.Auth{
		User:     *username,
		Password: *password,
	}
	proxyServer := goproxy.NewProxyHttpServer()
	proxyServer.ConnectDial = createSocksDialer(*socksAddr, auth)           // Routing HTTP request
	proxyServer.Tr.DialContext = createSocksDialerContext(*socksAddr, auth) // Routing HTTPS request
	proxyServer.Verbose = *verbose

	log.Fatalln(http.ListenAndServe(*addr, proxyServer))
}
