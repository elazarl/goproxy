package goproxy

import (
	"net/url"
	"os"
	"testing"
)

var proxytests = map[string]struct {
	noProxy          string
	envHttpsProxy    string
	customHttpsProxy string
	url              string
	expectProxy      string
}{
	"do not proxy without a proxy configured":   {"", "", "", "https://foo.bar/baz", ""},
	"proxy with a proxy configured":             {"", "daproxy", "", "https://foo.bar/baz", "http://daproxy:http"},
	"proxy without a scheme":                    {"", "daproxy", "", "//foo.bar/baz", "http://daproxy:http"},
	"proxy with a proxy configured with a port": {"", "http://daproxy:123", "", "https://foo.bar/baz", "http://daproxy:123"},
	"proxy with an https proxy configured":      {"", "https://daproxy", "", "https://foo.bar/baz", "https://daproxy:https"},
	"proxy with a non-matching no_proxy":        {"other.bar", "daproxy", "", "https://foo.bar/baz", "http://daproxy:http"},
	"do not proxy with a full no_proxy match":   {"foo.bar", "daproxy", "", "https://foo.bar/baz", ""},
	"do not proxy with a suffix no_proxy match": {".bar", "daproxy", "", "https://foo.bar/baz", ""},
	"proxy with an custom https proxy":          {"", "https://daproxy", "https://customproxy", "https://foo.bar/baz", "https://customproxy:https"},
}

var envKeys = []string{"no_proxy", "http_proxy", "https_proxy", "NO_PROXY", "HTTP_PROXY", "HTTPS_PROXY"}

func TestHttpsProxy(t *testing.T) {
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
			os.Setenv("https_proxy", spec.envHttpsProxy)

			url, err := url.Parse(spec.url)
			if err != nil {
				t.Fatalf("bad test input URL %s: %v", spec.url, err)
			}

			actual, err := httpsProxy(url, spec.customHttpsProxy)
			if err != nil {
				t.Fatalf("unexpected error parsing proxy from env: %#v", err)
			}
			if actual != spec.expectProxy {
				t.Errorf("expected proxy url '%s' but got '%s'", spec.expectProxy, actual)
			}

			os.Unsetenv("no_proxy")
			os.Unsetenv("https_proxy")
		})
	}
}
