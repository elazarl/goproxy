package main

import (
	"context"
	"github.com/things-go/go-socks5"
	"log"
	"net"
	"os"
)

func main() {
	// Create a SOCKS5 server
	server := socks5.NewServer(
		socks5.WithLogger(socks5.NewLogger(log.New(os.Stdout, "socks5: ", log.LstdFlags))),
		socks5.WithDialAndRequest(func(ctx context.Context, network, addr string, request *socks5.Request) (net.Conn, error) {
			log.Printf("Request from %s to %s", request.RemoteAddr, request.DestAddr)
			return net.Dial(network, addr)
		}),
	)

	// Create SOCKS5 proxy on localhost port 1080
	if err := server.ListenAndServe("tcp", ":1080"); err != nil {
		panic(err)
	}
}
