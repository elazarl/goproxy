package goproxy_test

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/elazarl/goproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebSocketMitm(t *testing.T) {
	// Start a WebSocket echo server
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		defer func() {
			_ = c.Close(websocket.StatusNormalClosure, "")
		}()

		ctx := r.Context()
		for {
			mt, message, err := c.Read(ctx)
			if err != nil {
				break
			}
			err = c.Write(ctx, mt, append([]byte("ECHO: "), message...))
			if err != nil {
				break
			}
		}
	}))
	backend.StartTLS()
	defer backend.Close()

	// Start goproxy
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)

	proxyServer := httptest.NewServer(proxy)
	defer proxyServer.Close()

	// Configure WebSocket client to use proxy
	proxyURL, err := url.Parse(proxyServer.URL)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, backend.URL, &websocket.DialOptions{
		HTTPClient: &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
		},
	})
	require.NoError(t, err)
	defer func() {
		_ = c.Close(websocket.StatusNormalClosure, "")
	}()

	// Verify bidirectional communication
	message := []byte("Hello WebSocket")
	err = c.Write(ctx, websocket.MessageText, message)
	require.NoError(t, err)

	mt, response, err := c.Read(ctx)
	require.NoError(t, err)

	assert.Equal(t, websocket.MessageText, mt)
	assert.Equal(t, "ECHO: Hello WebSocket", string(response))
}
