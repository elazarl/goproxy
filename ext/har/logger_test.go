
package har_test

import (
    "bytes"
    "encoding/json"
    "io"
    "net/http"
    "net/http/httptest"
    "net/url"
    "os"
    "testing"

    "github.com/elazarl/goproxy"
    "github.com/elazarl/goproxy/ext/har"
)

type ConstantHandler string

func (h ConstantHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    io.WriteString(w, string(h))
}

func oneShotProxy(proxy *goproxy.ProxyHttpServer) (client *http.Client, s *httptest.Server) {
    s = httptest.NewServer(proxy)

    proxyUrl, _ := url.Parse(s.URL)
    tr := &http.Transport{Proxy: http.ProxyURL(proxyUrl)}
    client = &http.Client{Transport: tr}
    return
}

func TestHarLogger(t *testing.T) {
    // Create a response we expect
    expected := "hello world"
    background := httptest.NewServer(ConstantHandler(expected))
    defer background.Close()

    // Set up the proxy with HAR logger
    proxy := goproxy.NewProxyHttpServer()
    logger := har.NewLogger()
    logger.SetCaptureContent(true)

    proxy.OnRequest().DoFunc(logger.OnRequest)
    proxy.OnResponse().DoFunc(logger.OnResponse)

    client, proxyserver := oneShotProxy(proxy)
    defer proxyserver.Close()

    // Make a request
    resp, err := client.Get(background.URL)
    if err != nil {
        t.Fatal(err)
    }

    // Read the response
    msg, err := io.ReadAll(resp.Body)
    if err != nil {
        t.Fatal(err)
    }
    resp.Body.Close()

    if string(msg) != expected {
        t.Errorf("Expected '%s', actual '%s'", expected, string(msg))
    }

    // Test POST request with content
    postData := "test=value"
    req, err := http.NewRequest("POST", background.URL, bytes.NewBufferString(postData))
    if err != nil {
        t.Fatal(err)
    }
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
    
    resp, err = client.Do(req)
    if err != nil {
        t.Fatal(err)
    }
    resp.Body.Close()

    // Save HAR file and verify content
    tmpfile := "test.har"
    err = logger.SaveToFile(tmpfile)
    if err != nil {
        t.Fatal(err)
    }
    defer os.Remove(tmpfile)

    // Read and verify HAR content
    harData, err := os.ReadFile(tmpfile)
    if err != nil {
        t.Fatal(err)
    }

    var harLog har.Har
    if err := json.Unmarshal(harData, &harLog); err != nil {
        t.Fatal(err)
    }

    // Verify we captured both requests
    if len(harLog.Log.Entries) != 2 {
        t.Errorf("Expected 2 entries in HAR log, got %d", len(harLog.Log.Entries))
    }

    // Verify GET request
    if harLog.Log.Entries[0].Request.Method != "GET" {
        t.Errorf("Expected GET request, got %s", harLog.Log.Entries[0].Request.Method)
    }

    // Verify POST request
    if harLog.Log.Entries[1].Request.Method != "POST" {
        t.Errorf("Expected POST request, got %s", harLog.Log.Entries[1].Request.Method)
    }
}
