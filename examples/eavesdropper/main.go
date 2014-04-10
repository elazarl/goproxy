package main

import (
	"bufio"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/elazarl/goproxy"
	"github.com/elazarl/goproxy/ext/html"
)

func orPanic(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	proxy := goproxy.NewProxyHttpServer()

	goproxy.HijackConnect.Hijack = func(req *http.Request, client net.Conn, ctx *goproxy.ProxyCtx) {
		defer func() {
			if e := recover(); e != nil {
				ctx.Logf("error connecting to remote: %v", e)
				client.Write([]byte("HTTP/1.1 500 Cannot reach destination\r\n\r\n"))
			}
			client.Close()
		}()
		clientBuf := bufio.NewReadWriter(bufio.NewReader(client), bufio.NewWriter(client))
		remote, err := net.Dial("tcp", req.URL.Host)
		orPanic(err)
		remoteBuf := bufio.NewReadWriter(bufio.NewReader(remote), bufio.NewWriter(remote))
		for {
			req, err := http.ReadRequest(clientBuf.Reader)
			orPanic(err)
			orPanic(req.Write(remoteBuf))
			orPanic(remoteBuf.Flush())
			resp, err := http.ReadResponse(remoteBuf.Reader, req)
			orPanic(err)

			resp = proxy.FilterResponse(resp, ctx)

			orPanic(resp.Write(clientBuf.Writer))
			orPanic(clientBuf.Flush())
		}
	}

	proxy.OnRequest().HandleConnectFunc(
		func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
			// hijack all http connect
			if !strings.HasSuffix(host, ":443") && ctx.Req.Method == "CONNECT" {
				return goproxy.HijackConnect, host
			}
			if strings.HasSuffix(host, ":443") {
				return goproxy.MitmConnect, host
			}
			return goproxy.OkConnect, host
		})
	proxy.OnResponse(goproxy_html.IsHtml).Do(goproxy_html.HandleString(func(s string, ctx *goproxy.ProxyCtx) string {
		return s + "<script>alert(1)</script>"
	}))
	proxy.Verbose = true
	log.Fatal(http.ListenAndServe(":7000", proxy))
}
