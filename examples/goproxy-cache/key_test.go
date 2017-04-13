package main_test

import (
	"net/url"
	"testing"

	httpcache "github.com/elazarl/goproxy/examples/goproxy-cache"
	"github.com/stretchr/testify/assert"
)

func mustParseUrl(u string) *url.URL {
	ru, err := url.Parse(u)
	if err != nil {
		panic(err)
	}
	return ru
}

func TestKeysDiffer(t *testing.T) {
	k1 := httpcache.NewKey("GET", mustParseUrl("http://x.org/test"), nil)
	k2 := httpcache.NewKey("GET", mustParseUrl("http://y.org/test"), nil)

	assert.NotEqual(t, k1.String(), k2.String())
}

func TestRequestKey(t *testing.T) {
	r := newRequest("GET", "http://x.org/test")

	k1 := httpcache.NewKey("GET", mustParseUrl("http://x.org/test"), nil)
	k2 := httpcache.NewRequestKey(r)

	assert.Equal(t, k1.String(), k2.String())
}

func TestVaryKey(t *testing.T) {
	r := newRequest("GET", "http://x.org/test", "Llamas-1: true", "Llamas-2: false")

	k1 := httpcache.NewRequestKey(r)
	k2 := httpcache.NewRequestKey(r).Vary("Llamas-1, Llamas-2", r)

	assert.NotEqual(t, k1.String(), k2.String())
}

func TestRequestKeyWithContentLocation(t *testing.T) {
	r := newRequest("GET", "http://x.org/test1", "Content-Location: http://x.org/test2")

	k1 := httpcache.NewKey("GET", mustParseUrl("http://x.org/test2"), nil)
	k2 := httpcache.NewRequestKey(r)

	assert.Equal(t, k1.String(), k2.String())
}

func TestRequestKeyWithIllegalContentLocation(t *testing.T) {
	r := newRequest("GET", "http://x.org/test1", "Content-Location: http://y.org/test2")

	k1 := httpcache.NewKey("GET", mustParseUrl("http://x.org/test1"), nil)
	k2 := httpcache.NewRequestKey(r)

	assert.Equal(t, k1.String(), k2.String())
}
