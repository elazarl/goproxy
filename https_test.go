package goproxy

import (
	"net/url"
	"os"
	"testing"
)

var proxytests = map[string]struct {
	noProxy     string
	httpsProxy  string
	url         string
	expectProxy string
}{
	"never proxy http": {"", "daproxy", "http://foo.bar/baz", ""},

	"do not proxy https without a proxy configured":   {"", "", "https://foo.bar/baz", ""},
	"proxy https with a proxy configured":             {"", "daproxy", "https://foo.bar/baz", "daproxy:http"},
	"proxy https with a proxy configured with a port": {"", "http://daproxy:123", "https://foo.bar/baz", "daproxy:123"},
	"proxy https with an https proxy configured":      {"", "https://daproxy", "https://foo.bar/baz", "daproxy:https"},
	"proxy https with a non-matching no_proxy":        {"other.bar", "daproxy", "https://foo.bar/baz", "daproxy:http"},
	"do not proxy https with a full no_proxy match":   {"foo.bar", "daproxy", "https://foo.bar/baz", ""},
	"do not proxy https with a suffix no_proxy match": {".bar", "daproxy", "https://foo.bar/baz", ""},
}

var envKeys = []string{"no_proxy", "http_proxy", "https_proxy", "NO_PROXY", "HTTP_PROXY", "HTTPS_PROXY"}

func TestHttpsProxyFromEnv(t *testing.T) {
	for _, k := range envKeys {
		v, ok := os.LookupEnv(k)
		if ok {
			defer func() {
				os.Setenv(k, v)
			}()
			os.Unsetenv(k)
		} else {
			defer func() {
				os.Unsetenv(k)
			}()
		}
	}
	os.Setenv("http_proxy", "should.never.use.this")

	for name, spec := range proxytests {
		t.Run(name, func(t *testing.T) {
			os.Setenv("no_proxy", spec.noProxy)
			os.Setenv("https_proxy", spec.httpsProxy)

			url, err := url.Parse(spec.url)
			if err != nil {
				t.Fatalf("bad test input URL %s: %v", spec.url, err)
			}

			actual, err := httpsProxyFromEnv(url)
			if err != nil {
				t.Fatalf("unexpected error parsing proxy from env: %#v", err)
			}
			if actual != spec.expectProxy {
				t.Errorf("expected proxy url '%s' but got '%s'", spec.expectProxy, actual)
			}
		})
	}
}
