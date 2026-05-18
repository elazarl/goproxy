package main

import (
	"context"
	"crypto/tls"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"time"

	"github.com/coder/websocket"
	"github.com/elazarl/goproxy"
)

const (
	echoAddr  = ":12345"
	proxyAddr = ":54321"
	proxyURL  = "http://localhost:54321"
	echoURL   = "wss://localhost:12345"
)

func echo(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		log.Printf("upgrade: %v\n", err)
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	ctx := context.Background()
	for {
		mt, message, err := c.Read(ctx)
		if err != nil {
			log.Printf("read: %v\n", err)
			break
		}
		log.Printf("recv: %s\n", message)
		if err := c.Write(ctx, mt, message); err != nil {
			log.Printf("write: %v\n", err)
			break
		}
	}
}

func startEchoServer() {
	log.Println("Starting echo server")
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/", echo)
		err := http.ListenAndServeTLS(echoAddr, "localhost.pem", "localhost-key.pem", mux)
		if err != nil {
			log.Fatal(err)
		}
	}()
}

func startProxy() {
	log.Println("Starting proxy server")
	go func() {
		proxy := goproxy.NewProxyHttpServer()
		proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
		proxy.Verbose = true

		if err := http.ListenAndServe(proxyAddr, proxy); err != nil {
			log.Fatal(err)
		}
	}()
}

func main() {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	startEchoServer()
	startProxy()

	parsedProxy, err := url.Parse(proxyURL)
	if err != nil {
		log.Fatal("unable to parse proxy URL:", err)
	}

	ctx := context.Background()
	c, _, err := websocket.Dial(ctx, echoURL, &websocket.DialOptions{
		HTTPClient: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
				Proxy: http.ProxyURL(parsedProxy),
			},
		},
		Subprotocols: []string{"p1"},
	})
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			_, message, err := c.Read(ctx)
			if err != nil {
				log.Println("read:", err)
				return
			}
			log.Printf("recv: %s", message)
		}
	}()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case t := <-ticker.C: // Message send
			// Write current time to the websocket client every 1 second
			if err := c.Write(ctx, websocket.MessageText, []byte(t.String())); err != nil {
				log.Println("write:", err)
				return
			}
		case <-interrupt: // Server shutdown
			log.Println("interrupt")
			// To cleanly close a connection, a client should send a close
			// frame and wait for the server to close the connection.
			err := c.Close(websocket.StatusNormalClosure, "")
			if err != nil {
				log.Println("write close:", err)
				return
			}

			select {
			case <-done:
			case <-time.After(time.Second):
			}
			return
		}
	}
}
