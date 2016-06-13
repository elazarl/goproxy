package main

import (
	"crypto/tls"
	"github.com/ecordell/goproxy"
	"github.com/gorilla/websocket"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"time"
)

var upgrader = websocket.Upgrader{} // use default options

func echo(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Print("upgrade:", err)
		return
	}
	defer c.Close()
	for {
		mt, message, err := c.ReadMessage()
		if err != nil {
			log.Println("read:", err)
			break
		}
		log.Printf("recv: %s", message)
		err = c.WriteMessage(mt, message)
		if err != nil {
			log.Println("write:", err)
			break
		}
	}
}

func StartEchoServer(wg *sync.WaitGroup) {
	log.Println("Starting echo server")
	wg.Add(1)
	go func() {
		http.HandleFunc("/", echo)
		err := http.ListenAndServeTLS(":12345", "localhost.pem", "localhost-key.pem", nil)
		if err != nil {
			panic("ListenAndServe: " + err.Error())
		}
		wg.Done()
	}()
}

func StartProxy(wg *sync.WaitGroup) {
	log.Println("Starting proxy server")
	wg.Add(1)
	go func() {
		proxy := goproxy.NewProxyHttpServer()
		proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
		proxy.Verbose = true

		err := http.ListenAndServe(":54321", proxy)
		if err != nil {
			log.Fatal(err.Error())
		}
		wg.Done()
	}()
}

func main() {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	wg := &sync.WaitGroup{}
	StartEchoServer(wg)
	StartProxy(wg)

	endpointUrl := "wss://localhost:12345"
	proxyUrl := "wss://localhost:54321"

	surl, _ := url.Parse(proxyUrl)
	dialer := websocket.Dialer{
		Subprotocols:    []string{"p1"},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		Proxy:           http.ProxyURL(surl),
	}

	c, _, err := dialer.Dial(endpointUrl, nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()

	done := make(chan struct{})

	go func() {
		defer c.Close()
		defer close(done)
		for {
			_, message, err := c.ReadMessage()
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
		case t := <-ticker.C:
			err := c.WriteMessage(websocket.TextMessage, []byte(t.String()))
			if err != nil {
				log.Println("write:", err)
				return
			}
		case <-interrupt:
			log.Println("interrupt")
			// To cleanly close a connection, a client should send a close
			// frame and wait for the server to close the connection.
			err := c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				log.Println("write close:", err)
				return
			}
			select {
			case <-done:
			case <-time.After(time.Second):
			}
			c.Close()
			return
		}
	}
}
