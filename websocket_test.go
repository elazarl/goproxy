package goproxy_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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
	proxy.Tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
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

// maskedTextFrame builds a client WebSocket text frame. Client frames must be
// masked, so we mask the payload with a fixed key.
func maskedTextFrame(payload string) []byte {
	mask := []byte{0x12, 0x34, 0x56, 0x78}
	frame := []byte{0x81, byte(0x80 | len(payload))}
	frame = append(frame, mask...)
	for i := 0; i < len(payload); i++ {
		frame = append(frame, payload[i]^mask[i%len(mask)])
	}
	return frame
}

// readHTTPHead reads one HTTP head one byte at a time, so that no byte after
// the head is consumed into a buffer that the caller then drops.
func readHTTPHead(t *testing.T, r io.Reader) string {
	t.Helper()

	var head []byte
	buf := make([]byte, 1)
	for !strings.HasSuffix(string(head), "\r\n\r\n") {
		_, err := io.ReadFull(r, buf)
		require.NoError(t, err)
		head = append(head, buf[0])
	}
	return string(head)
}

func TestWebSocketMitmEarlyClientFrame(t *testing.T) {
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
	proxy.Tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)

	proxyServer := httptest.NewServer(proxy)
	defer proxyServer.Close()

	backendURL, err := url.Parse(backend.URL)
	require.NoError(t, err)
	proxyURL, err := url.Parse(proxyServer.URL)
	require.NoError(t, err)

	// A raw client is required here: the bug only shows up when the first
	// WebSocket frame is sent in the same write as the upgrade request.
	var dialer net.Dialer
	raw, err := dialer.DialContext(context.Background(), "tcp", proxyURL.Host)
	require.NoError(t, err)
	defer func() {
		_ = raw.Close()
	}()
	require.NoError(t, raw.SetDeadline(time.Now().Add(5*time.Second)))

	_, err = fmt.Fprintf(raw, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", backendURL.Host, backendURL.Host)
	require.NoError(t, err)
	require.Contains(t, readHTTPHead(t, raw), " 200 ")

	conn := tls.Client(raw, &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         backendURL.Hostname(),
	})
	require.NoError(t, conn.HandshakeContext(context.Background()))

	upgrade := "GET / HTTP/1.1\r\n" +
		"Host: " + backendURL.Host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"\r\n"
	_, err = conn.Write(append([]byte(upgrade), maskedTextFrame("Hello")...))
	require.NoError(t, err)

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)

	// Server to client frames are not masked
	header := make([]byte, 2)
	_, err = io.ReadFull(br, header)
	require.NoError(t, err)
	assert.Equal(t, byte(0x81), header[0])

	payload := make([]byte, int(header[1]&0x7f))
	_, err = io.ReadFull(br, payload)
	require.NoError(t, err)

	assert.Equal(t, "ECHO: Hello", string(payload))
}
