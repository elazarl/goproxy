package goproxy_test

// build +linux

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/hazcod/goproxy"
)

func oneShotProxyNoKeepalive(proxy *goproxy.ProxyHttpServer, t *testing.T) (client *http.Client, s *httptest.Server) {
	s = httptest.NewServer(proxy)

	proxyUrl, _ := url.Parse(s.URL)
	tr := &http.Transport{TLSClientConfig: acceptAllCerts, Proxy: http.ProxyURL(proxyUrl), DisableKeepAlives: true}
	client = &http.Client{Transport: tr}
	return
}

func printfds(msg string, t *testing.T) int {
	fd, _ := os.Open("/proc/self/fd")
	fds, _ := fd.Readdir(-1)
	fd.Close()
	names := []string{}
	links := []string{}
	for _, f := range fds {
		names = append(names, f.Name())
		link, _ := os.Readlink("/proc/self/fd/" + f.Name())
		links = append(links, link)
	}
	lines := []string{}
	for i := range names {
		lines = append(lines, fmt.Sprintf("%2v → %v", names[i], links[i]))
	}
	t.Logf("[%s] /proc/self/fd:\n\t%s", msg, strings.Join(lines, "\n\t"))
	return len(fds)
}
func TestFDCountConnect(t *testing.T) {

	proxy := goproxy.NewProxyHttpServer()
	althttps := httptest.NewTLSServer(ConstantHanlder("althttps"))

	proxy.OnRequest().HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		u, _ := url.Parse(althttps.URL)
		printfds("in handler", t)
		return goproxy.OkConnect, u.Host
	})

	before := printfds("before", t)

	for i := range "12345" {
		pre := fmt.Sprintf("call %d", i+1)
		printfds(pre+", before", t)
		client, l := oneShotProxyNoKeepalive(proxy, t)
		if resp := string(getOrFail(https.URL+"/alturl", client, t)); resp != "althttps" {
			t.Error("Proxy should redirect CONNECT requests to local althttps server, expected 'althttps' got ", resp)
		}
		l.Close()
		printfds(pre+", after", t)
	}

	after := printfds("after", t)

	if before != after {
		t.Errorf("#FD before ≠ after! FD before: %d, after: %d", before, after)
	}
}