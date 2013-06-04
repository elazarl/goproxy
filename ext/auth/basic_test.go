package auth_test

import (
	"encoding/base64"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"testing"

	"github.com/elazarl/goproxy"
	"github.com/elazarl/goproxy/ext/auth"
)

type ConstantHanlder string

func (h ConstantHanlder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, string(h))
}

func oneShotProxy() (client *http.Client, proxy *goproxy.ProxyHttpServer, s *httptest.Server) {
	proxy = goproxy.NewProxyHttpServer()
	s = httptest.NewServer(proxy)

	proxyUrl, _ := url.Parse(s.URL)
	tr := &http.Transport{Proxy: http.ProxyURL(proxyUrl)}
	client = &http.Client{Transport: tr}
	return
}

func times(n int, s string) string {
	r := make([]byte, 0, n*len(s))
	for i := 0; i < n; i++ {
		r = append(r, s...)
	}
	return string(r)
}

func TestBasicAuthWithCurl(t *testing.T) {
	expected := ":c>"
	background := httptest.NewServer(ConstantHanlder(expected))
	defer background.Close()
	_, proxy, proxyserver := oneShotProxy()
	defer proxyserver.Close()
	proxy.OnRequest().Do(auth.Basic("my_realm", func(user, passwd string) bool {
		return user == "user" && passwd == "open sesame"
	}))

	cmd := exec.Command("curl",
		"--silent", "--show-error",
		"-x", proxyserver.URL,
		"-U", "user:open sesame",
		"--url", background.URL + "/[1-3]",
	)
	out, err := cmd.CombinedOutput() // if curl got error, it'll show up in stderr
	if err != nil {
		t.Fatal(err)
	}
	finalexpected := times(3, expected)
	if string(out) != finalexpected {
		t.Error("Expected", finalexpected, "got", string(out))
	}
}

func TestBasicAuth(t *testing.T) {
	expected := "hello"
	background := httptest.NewServer(ConstantHanlder(expected))
	defer background.Close()
	client, proxy, proxyserver := oneShotProxy()
	defer proxyserver.Close()
	proxy.OnRequest().Do(auth.Basic("my_realm", func(user, passwd string) bool {
		return user == "user" && passwd == "open sesame"
	}))

	// without auth
	resp, err := client.Get(background.URL)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("Proxy-Authenticate") != "Basic realm=my_realm" {
		t.Error("Expected Proxy-Authenticate header got", resp.Header.Get("Proxy-Authenticate"))
	}
	if resp.StatusCode != 407 {
		t.Error("Expected status 407 Proxy Authentication Required, got", resp.Status)
	}

	// with auth
	req, err := http.NewRequest("GET", background.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Proxy-Authorization",
		"Basic " + base64.StdEncoding.EncodeToString([]byte("user:open sesame")))
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Error("Expected status 200 OK, got", resp.Status)
	}
	msg, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(msg) != "hello" {
		t.Errorf("Expected '%s', actual '%s'", expected, string(msg))
	}
}
