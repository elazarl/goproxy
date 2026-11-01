package goproxy_test

import (
	"bufio"
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

	"github.com/elazarl/goproxy"
	"github.com/stretchr/testify/require"
)

func TestMitmSendsContinueBeforeReadingBody(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		useTLS                  bool
		expect                  string
		preventCanonicalization bool
	}{
		{"cleartext/single", false, "Expect: 100-continue\r\n", false},
		{"TLS/single", true, "Expect: 100-continue\r\n", false},
		{"cleartext/combined", false, "Expect: 100-continue, 100-CONTINUE\r\n", false},
		{"TLS/combined", true, "Expect: 100-continue, 100-CONTINUE\r\n", false},
		{"cleartext/repeated", false, "Expect: 100-continue\r\nExpect: 100-continue\r\n", false},
		{"TLS/repeated", true, "Expect: 100-continue\r\nExpect: 100-continue\r\n", false},
		{"cleartext/noncanonical", false, "expect: 100-continue, 100-CONTINUE\r\n", true},
		{"TLS/noncanonical", true, "eXpEcT: 100-continue\r\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Repeat("x", 80_000)
			received := make(chan string, 1)
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, err := io.ReadAll(r.Body)
				if err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				received <- string(data)
				w.WriteHeader(http.StatusCreated)
			})

			upstream := httptest.NewUnstartedServer(handler)
			if tc.useTLS {
				upstream.StartTLS()
			} else {
				upstream.Start()
			}
			defer upstream.Close()
			upstreamURL, err := url.Parse(upstream.URL)
			require.NoError(t, err)

			proxy := goproxy.NewProxyHttpServer()
			proxy.PreventCanonicalization = tc.preventCanonicalization
			proxy.Tr.TLSClientConfig = &tls.Config{
				InsecureSkipVerify: true,
			}
			proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
			proxy.OnRequest().DoFunc(func(req *http.Request, _ *goproxy.ProxyCtx) (*http.Request, *http.Response) {
				if req.URL.Path == "/reject" {
					return nil, goproxy.NewResponse(req, goproxy.ContentTypeText, http.StatusForbidden, "blocked")
				}
				return req, nil
			})
			proxyServer := httptest.NewServer(proxy)
			defer proxyServer.Close()

			raw, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", proxyServer.Listener.Addr().String())
			require.NoError(t, err)
			defer raw.Close()
			require.NoError(t, raw.SetDeadline(time.Now().Add(3*time.Second)))
			_, err = fmt.Fprintf(raw, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", upstreamURL.Host, upstreamURL.Host)
			require.NoError(t, err)
			connectResponse, err := http.ReadResponse(bufio.NewReader(raw), nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, connectResponse.StatusCode)
			require.NoError(t, connectResponse.Body.Close())

			var conn net.Conn
			if tc.useTLS {
				tlsConn := tls.Client(raw, &tls.Config{
					InsecureSkipVerify: true,
				})
				require.NoError(t, tlsConn.HandshakeContext(t.Context()))
				conn = tlsConn
			} else {
				conn = raw
			}
			_, err = fmt.Fprintf(conn,
				"PUT /upload HTTP/1.1\r\nHost: %s\r\nContent-Length: %d\r\n%s\r\n",
				upstreamURL.Host, len(body), tc.expect)
			require.NoError(t, err)
			reader := bufio.NewReader(conn)
			interim, err := http.ReadResponse(reader, nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusContinue, interim.StatusCode)
			require.NoError(t, interim.Body.Close())

			_, err = io.WriteString(conn, body)
			require.NoError(t, err)
			final, err := http.ReadResponse(reader, nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusCreated, final.StatusCode)
			require.NoError(t, final.Body.Close())
			require.Equal(t, body, <-received)

			_, err = fmt.Fprintf(conn,
				"PUT /reject HTTP/1.1\r\nHost: %s\r\nContent-Length: %d\r\nExpect: 100-continue\r\n\r\n",
				upstreamURL.Host, len(body))
			require.NoError(t, err)
			rejected, err := http.ReadResponse(reader, nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusForbidden, rejected.StatusCode)
			rejectedBody, err := io.ReadAll(rejected.Body)
			require.NoError(t, err)
			require.Equal(t, "blocked", string(rejectedBody))
			require.NoError(t, rejected.Body.Close())
			// No 100 Continue was sent, so the client will not send the body.
			// The proxy must announce the close instead of leaving the
			// connection blocked waiting for bytes that never arrive.
			require.True(t, rejected.Close, "final response without 100 Continue must close the connection")
			_, err = reader.ReadByte()
			require.ErrorIs(t, err, io.EOF)
		})
	}
}

func TestMitmDoesNotSendContinueAfterFinalResponse(t *testing.T) {
	upstream, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer upstream.Close()
	hold := make(chan struct{})
	defer close(hold)
	go func() {
		conn, acceptErr := upstream.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				return
			}
			if line == "\r\n" {
				break
			}
		}
		_, _ = io.WriteString(conn, "HTTP/1.1 403 Forbidden\r\nContent-Length: 6\r\n\r\ndenied")
		<-hold
	}()

	proxy := goproxy.NewProxyHttpServer()
	proxy.Tr.ExpectContinueTimeout = time.Second
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxyServer := httptest.NewServer(proxy)
	defer proxyServer.Close()

	raw, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", proxyServer.Listener.Addr().String())
	require.NoError(t, err)
	defer raw.Close()
	require.NoError(t, raw.SetDeadline(time.Now().Add(3*time.Second)))
	_, err = fmt.Fprintf(raw, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", upstream.Addr(), upstream.Addr())
	require.NoError(t, err)
	reader := bufio.NewReader(raw)
	connectResponse, err := http.ReadResponse(reader, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, connectResponse.StatusCode)
	require.NoError(t, connectResponse.Body.Close())

	_, err = fmt.Fprintf(raw,
		"PUT /upload HTTP/1.1\r\nHost: %s\r\nContent-Length: 80000\r\nExpect: 100-continue\r\n\r\n",
		upstream.Addr())
	require.NoError(t, err)
	response, err := http.ReadResponse(reader, nil)
	require.NoError(t, err)
	if response.StatusCode == http.StatusContinue {
		require.NoError(t, response.Body.Close())
		_, err = io.WriteString(raw, strings.Repeat("x", 80_000))
		require.NoError(t, err)
		response, err = http.ReadResponse(reader, nil)
		require.NoError(t, err)
	}
	require.Equal(t, http.StatusForbidden, response.StatusCode)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, "denied", string(body))
	require.NoError(t, response.Body.Close())
}
