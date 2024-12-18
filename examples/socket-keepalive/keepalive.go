package main

import (
	"context"
	"flag"
	"github.com/elazarl/goproxy"
	"log"
	"net"
	"net/http"
)

func main() {
	verbose := flag.Bool("v", false, "should every proxy request be logged to stdout")
	addr := flag.String("addr", ":8080", "proxy listen address")
	flag.Parse()
	proxy := goproxy.NewProxyHttpServer()
	proxy.Tr.DialContext = func(ctx context.Context, network, addr string) (c net.Conn, err error) {
		var d net.Dialer
		c, err = d.DialContext(ctx, network, addr)
		if c, ok := c.(*net.TCPConn); err == nil && ok {
			c.SetKeepAlive(true)
			go func() {
				<-ctx.Done()
				c.Close()
			}()
		}
		return
	}
	proxy.Verbose = *verbose
	log.Fatal(http.ListenAndServe(*addr, proxy))
}
