package main

import (
	"github.com/elazarl/goproxy"
	"github.com/elazarl/goproxy/ext/html"
	"io"
	"log"
	. "net/http"
	"time"
)

type Count struct {
	Id    string
	Count int64
}
type CountReadCloser struct {
	Id string
	R  io.ReadCloser
	ch chan<- Count
	nr int64
}

func (c *CountReadCloser) Read(b []byte) (n int, err error) {
	n, err = c.R.Read(b)
	c.nr += int64(n)
	return
}
func (c CountReadCloser) Close() error {
	c.ch <- Count{c.Id, c.nr}
	return c.R.Close()
}

func main() {
	proxy := goproxy.NewProxyHttpServer()
	//proxy.Verbose = true
	timer := make(chan bool)
	ch := make(chan Count, 10)
	go func() {
		for {
			time.Sleep(time.Minute * 2)
			timer <- true
		}
	}()
	go func() {
		m := make(map[string]int64)
		for {
			select {
			case c := <-ch:
				m[c.Id] = m[c.Id] + c.Count
			case <-timer:
				println("statistics")
				for k, v := range m {
					println(k, "->", v)
				}
			}
		}
	}()
	proxy.OnResponse(goproxy_html.IsWebRelatedText).DoFunc(func(resp *Response, ctx *goproxy.ProxyCtx) *Response {
		resp.Body = &CountReadCloser{ctx.Req.URL.String(), resp.Body, ch, 0}
		return resp
	})
	log.Fatal(ListenAndServe(":8080", proxy))
}
