package goproxy_test

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elazarl/goproxy"
	"github.com/elazarl/goproxy/internal/testutil"
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
		name        string
		method      string
		status      int
		replaceBody bool
	}{
		{name: "no content", method: http.MethodGet, status: http.StatusNoContent},
		{name: "not modified", method: http.MethodGet, status: http.StatusNotModified},
		{name: "head", method: http.MethodHead, status: http.StatusOK},
		{name: "no content with replaced body", method: http.MethodGet, status: http.StatusNoContent, replaceBody: true},
		{name: "not modified with replaced body", method: http.MethodGet, status: http.StatusNotModified, replaceBody: true},
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
				if tc.replaceBody {
					proxy.OnResponse().DoFunc(func(resp *http.Response, _ *goproxy.ProxyCtx) *http.Response {
						resp.Body = io.NopCloser(strings.NewReader(""))
						return resp
					})
				}

				client, _ := testutil.NewProxy(t, proxy)
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

				// Exercise a subsequent request on the client's persistent transport.
				second := do()
				assert.Equal(t, tc.status, second.StatusCode)
			})
		}
	}
}

func TestMitmBodilessDirectResponseIsNotChunked(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("direct response unexpectedly reached the upstream server")
	}))
	defer upstream.Close()

	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.OnRequest().DoFunc(func(req *http.Request, _ *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		return nil, goproxy.NewResponse(req, goproxy.ContentTypeText, http.StatusOK, "")
	})

	client, _ := testutil.NewProxy(t, proxy)
	defer client.CloseIdleConnections()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodHead, upstream.URL, http.NoBody)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Empty(t, resp.TransferEncoding,
		"a direct response to HEAD must not be framed as chunked")
}
