package goproxy_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elazarl/goproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// After a MITM'd response, the proxy must close the client connection when
// the request or the response carries "Connection: close". A client that
// doesn't read the body of such a response waits for the close to finish.
func TestMitmConnectionClose(t *testing.T) {
	cases := []struct {
		name           string
		requestClose   bool
		responseClose  bool
		directResponse bool
	}{
		{name: "keep-alive"},
		{name: "request close", requestClose: true},
		{name: "response close", responseClose: true},
		// A handler that returns a canned response also returns a nil request,
		// so the close has to come from the client's original request.
		{name: "request close with direct response", requestClose: true, directResponse: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				assert.False(t, tc.directResponse, "direct response unexpectedly reached the upstream server")
				if tc.responseClose {
					w.Header().Set("Connection", "close")
				}
				_, _ = io.WriteString(w, "ok")
			}))
			defer upstream.Close()

			proxy := goproxy.NewProxyHttpServer()
			proxy.Tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
			proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
			if tc.directResponse {
				proxy.OnRequest().DoFunc(func(req *http.Request, _ *goproxy.ProxyCtx) (*http.Request, *http.Response) {
					return nil, goproxy.NewResponse(req, goproxy.ContentTypeText, http.StatusOK, "ok")
				})
			}
			proxySrv := httptest.NewServer(proxy)
			defer proxySrv.Close()

			// A raw client is required here: http.Transport closes the connection
			// itself on "Connection: close", so it can't tell whether the proxy did.
			conn, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", proxySrv.Listener.Addr().String())
			require.NoError(t, err)
			defer conn.Close()
			require.NoError(t, conn.SetDeadline(time.Now().Add(10*time.Second)))

			connectReq, err := http.NewRequestWithContext(context.Background(), http.MethodConnect, upstream.URL, nil)
			require.NoError(t, err)
			require.NoError(t, connectReq.Write(conn))
			connectResp, err := http.ReadResponse(bufio.NewReader(conn), connectReq)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, connectResp.StatusCode)

			tlsConn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true})
			require.NoError(t, tlsConn.HandshakeContext(context.Background()))
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, upstream.URL, http.NoBody)
			require.NoError(t, err)
			req.Close = tc.requestClose
			require.NoError(t, req.Write(tlsConn))

			reader := bufio.NewReader(tlsConn)
			resp, err := http.ReadResponse(reader, req)
			require.NoError(t, err)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Equal(t, "ok", string(body))

			require.NoError(t, tlsConn.SetReadDeadline(time.Now().Add(time.Second)))
			_, err = reader.ReadByte()
			if tc.requestClose || tc.responseClose {
				require.ErrorIs(t, err, io.EOF, "proxy kept the connection open after Connection: close")
			} else {
				var netErr net.Error
				require.ErrorAs(t, err, &netErr)
				assert.True(t, netErr.Timeout(), "proxy closed a keep-alive connection: %v", err)
			}
		})
	}
}
