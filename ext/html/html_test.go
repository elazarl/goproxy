package goproxy_html_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elazarl/goproxy"
	"github.com/elazarl/goproxy/ext/html"
	"github.com/elazarl/goproxy/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hebrewBytes is "דף" (DALET, PEH SOFIT) encoded in ISO-8859-8.
var hebrewBytes = []byte{0xe3, 0xf3}

func TestHandleStringConvertsCharsetToUTF8AndBack(t *testing.T) {
	background := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=iso-8859-8")
		_, _ = w.Write(hebrewBytes)
	}))
	t.Cleanup(background.Close)

	handled := make(chan string, 2)
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnResponse().Do(goproxy_html.HandleString(
		func(s string, _ *goproxy.ProxyCtx) string {
			handled <- s
			return s
		}))
	client, _ := testutil.NewProxy(t, proxy)

	body := testutil.GetOrFail(t, client, background.URL+"/cp1255.txt")

	assert.Equal(t, hebrewBytes, body, "response should be translated back to ISO-8859-8")

	require.Len(t, handled, 1, "HandleString should have been called once")
	assert.Equal(t, "דף", <-handled,
		"HandleString should get DALET & PEH SOFIT converted from ISO-8859-8 to utf-8")
}
