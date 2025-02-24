package main

import (
	"crypto/subtle"
	"encoding/base64"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/elazarl/goproxy"
	"github.com/elazarl/goproxy/ext/auth"
)

const _proxyAuthHeader = "Proxy-Authorization"

func SetBasicAuth(username, password string, req *http.Request) {
	req.Header.Set(_proxyAuthHeader, "Basic "+base64.StdEncoding.EncodeToString([]byte(username+":"+password)))
}

func main() {
	username, password := "foo", "bar"

	// Start end proxy server
	endProxy := goproxy.NewProxyHttpServer()
	endProxy.Verbose = true
	auth.ProxyBasic(endProxy, "my_realm", func(user, pwd string) bool {
		return subtle.ConstantTimeCompare([]byte(user), []byte(username)) == 1 &&
			subtle.ConstantTimeCompare([]byte(pwd), []byte(password)) == 1
	})
	log.Println("serving end proxy server at localhost:8082")
	go http.ListenAndServe("localhost:8082", endProxy)

	// Start middle proxy server
	middleProxy := goproxy.NewProxyHttpServer()
	middleProxy.Verbose = true
	middleProxy.Tr.Proxy = func(req *http.Request) (*url.URL, error) {
		// Here we specify the proxy URL of the other server.
		// If it was a socks5 proxy, we would have used an url like
		// socks5://localhost:8082
		return url.Parse("http://localhost:8082")
	}
	connectReqHandler := func(req *http.Request) {
		SetBasicAuth(username, password, req)
	}
	middleProxy.ConnectDial = middleProxy.NewConnectDialToProxyWithHandler("http://localhost:8082", connectReqHandler)

	middleProxy.OnRequest().Do(goproxy.FuncReqHandler(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		SetBasicAuth(username, password, req)
		return req, nil
	}))
	log.Println("serving middle proxy server at localhost:8081")
	go http.ListenAndServe("localhost:8081", middleProxy)

	time.Sleep(1 * time.Second)

	// Make a single HTTP request, from client to internet, through the 2 proxies
	middleProxyUrl := "http://localhost:8081"
	request, err := http.NewRequest(http.MethodGet, "https://ip.cn", nil)
	if err != nil {
		log.Fatalf("new request failed:%v", err)
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: func(req *http.Request) (*url.URL, error) {
				return url.Parse(middleProxyUrl)
			},
		},
	}
	resp, err := client.Do(request)
	if err != nil {
		log.Fatalf("get resp failed: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		log.Fatalf("status %d, data %s", resp.StatusCode, data)
	}

	log.Printf("resp: %s", data)
}
