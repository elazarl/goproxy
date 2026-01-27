package main

import (
	"context"
	"crypto/tls"
	"github.com/coder/websocket"
	"github.com/yx-zero/goproxy-transparent"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"time"
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

func StartEchoServer() {
	log.Println("Starting echo server")
	go func() {
		http.HandleFunc("/", echo)
		err := http.ListenAndServeTLS(":12345", "localhost.pem", "localhost-key.pem", nil)
		if err != nil {
			log.Fatal(err)
		}
	}()
}

func StartProxy() {
	log.Println("Starting proxy server")
	go func() {
		proxy := goproxy.NewProxyHttpServer()
		proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
		proxy.Verbose = true

		if err := http.ListenAndServe(":54321", proxy); err != nil {
			log.Fatal(err)
		}
	}()
}

func main() {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	StartEchoServer()
	StartProxy()

	proxyUrl := "http://localhost:54321"
	parsedProxy, err := url.Parse(proxyUrl)
	if err != nil {
		log.Fatal("unable to parse proxy URL")
	}

	ctx := context.Background()
	endpointUrl := "wss://localhost:12345"

	c, _, err := websocket.Dial(ctx, endpointUrl, &websocket.DialOptions{
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
