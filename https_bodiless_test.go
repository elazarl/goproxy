package goproxy_test

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/elazarl/goproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A bodiless response (204, 304, HEAD) must not be framed as chunked: clients
// stop at the header terminator, leaving the framing bytes to corrupt the next
// response on a kept-alive connection.
//
// Both upstream protocols matter: an empty HTTP/2 body is not http.NoBody, so
// an HTTP/1-only test proves nothing about the HTTP/2 path.
func TestMitmBodilessResponseIsNotChunked(t *testing.T) {
	cases := []struct {
		name   string
		method string
		status int
	}{
		{name: "no content", method: http.MethodGet, status: http.StatusNoContent},
		{name: "not modified", method: http.MethodGet, status: http.StatusNotModified},
		{name: "head", method: http.MethodHead, status: http.StatusOK},
	}

	for _, upstreamHTTP2 := range []bool{false, true} {
		for _, tc := range cases {
			name := tc.name
			if upstreamHTTP2 {
				name += " over http2 upstream"
			} else {
				name += " over http1 upstream"
			}

			t.Run(name, func(t *testing.T) {
				wantProtoMajor := 1
				if upstreamHTTP2 {
					wantProtoMajor = 2
				}
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// Without this the matrix silently collapses to six HTTP/1
					// cases if ALPN negotiation ever stops working.
					assert.Equal(t, wantProtoMajor, r.ProtoMajor)
					w.WriteHeader(tc.status)
				})

				var upstream *httptest.Server
				if upstreamHTTP2 {
					upstream = httptest.NewUnstartedServer(handler)
					upstream.EnableHTTP2 = true
					upstream.StartTLS()
				} else {
					upstream = httptest.NewTLSServer(handler)
				}
				defer upstream.Close()

				proxy := goproxy.NewProxyHttpServer()
				proxy.Tr = &http.Transport{
					ForceAttemptHTTP2: upstreamHTTP2,
					TLSClientConfig: &tls.Config{
						InsecureSkipVerify: true,
					},
				}
				if upstreamHTTP2 {
					proxy.Tr.TLSClientConfig.NextProtos = []string{"h2"}
				}
				proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)

				proxySrv := httptest.NewServer(proxy)
				defer proxySrv.Close()
				proxyURL, err := url.Parse(proxySrv.URL)
				require.NoError(t, err)

				client := &http.Client{Transport: &http.Transport{
					Proxy:           http.ProxyURL(proxyURL),
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
				}}
				defer client.CloseIdleConnections()

				do := func() *http.Response {
					req, err := http.NewRequestWithContext(context.Background(), tc.method, upstream.URL, http.NoBody)
					require.NoError(t, err)
					resp, err := client.Do(req)
					require.NoError(t, err)
					t.Cleanup(func() { _ = resp.Body.Close() })
					return resp
				}

				resp := do()
				require.Equal(t, tc.status, resp.StatusCode)
				assert.Empty(t, resp.TransferEncoding,
					"a response without a message body must not be framed as chunked")

				// Reuse proves no stray framing bytes were left behind.
				second := do()
				assert.Equal(t, tc.status, second.StatusCode)
			})
		}
	}
}
