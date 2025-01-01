package har

import (
    "encoding/json"
    "io"
    "net/http"
    "net/http/httptest"
    "net/url"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"

    "github.com/elazarl/goproxy"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

// ConstantHandler is a simple HTTP handler that returns a constant response
type ConstantHandler string

func (h ConstantHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "text/plain")
    io.WriteString(w, string(h))
}

// createTestProxy sets up a test proxy with a HAR logger
func createTestProxy(logger *Logger) *httptest.Server {
    proxy := goproxy.NewProxyHttpServer()
    proxy.OnRequest().DoFunc(logger.OnRequest)
    proxy.OnResponse().DoFunc(logger.OnResponse)
    return httptest.NewServer(proxy)
}

// createProxyClient creates an HTTP client that uses the given proxy
func createProxyClient(proxyURL string) *http.Client {
    proxyURLParsed, _ := url.Parse(proxyURL)
    tr := &http.Transport{
        Proxy: http.ProxyURL(proxyURLParsed),
    }
    return &http.Client{Transport: tr}
}


func TestHarLoggerBasicFunctionality(t *testing.T) {
    testCases := []struct {
        name           string
        method         string
        body           string
        contentType    string
        expectedMethod string
    }{
        {
            name:           "GET Request",
            method:         http.MethodGet,
            expectedMethod: http.MethodGet,
        },
        {
            name:           "POST Request",
            method:         http.MethodPost,
            body:           `{"test":"data"}`,
            contentType:    "application/json",
            expectedMethod: http.MethodPost,
        },
    }

    for _, tc := range testCases {
        t.Run(tc.name, func(t *testing.T) {
            background := httptest.NewServer(ConstantHandler("hello world"))
            defer background.Close()

            logger := NewLogger()
            proxyServer := createTestProxy(logger)
            defer proxyServer.Close()

            client := createProxyClient(proxyServer.URL)

            // Prepare request
            req, err := http.NewRequest(tc.method, background.URL, strings.NewReader(tc.body))
            require.NoError(t, err, "Should create request")
            if tc.contentType != "" {
                req.Header.Set("Content-Type", tc.contentType)
            }

            // Send request and capture response
            resp, err := client.Do(req)
            require.NoError(t, err, "Should send request successfully")
            defer resp.Body.Close()

            // Read response body
            bodyBytes, _ := io.ReadAll(resp.Body)
            body := string(bodyBytes)
            assert.Equal(t, "hello world", body, "Response body should match")

            time.Sleep(200 * time.Millisecond)

            // Verify HAR entry
            entries := logger.GetEntries()
            require.Len(t, entries, 1, "Should have one log entry")
            entry := entries[0]
            assert.Equal(t, tc.expectedMethod, entry.Request.Method, "Request method should match")
        })
    }
}

func TestHarLoggerHeaders(t *testing.T) {
    background := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("X-Test-Header", "test-value")
        w.Write([]byte("test"))
    }))
    defer background.Close()

    logger := NewLogger()

    proxyServer := createTestProxy(logger)
    defer proxyServer.Close()

    client := createProxyClient(proxyServer.URL)

    req, err := http.NewRequest("GET", background.URL, nil)
    require.NoError(t, err, "Should create request")
    req.Header.Set("X-Custom-Header", "custom-value")

    resp, err := client.Do(req)
    require.NoError(t, err, "Should send request")
    defer resp.Body.Close()

    time.Sleep(200 * time.Millisecond)

    entries := logger.GetEntries()
    require.Len(t, entries, 1, "Should have one log entry")
    entry := entries[0]

    // Convert headers to maps for easier checking
    reqHeaders := make(map[string]string)
    for _, h := range entry.Request.Headers {
        reqHeaders[h.Name] = h.Value
    }
    assert.Equal(t, "custom-value", reqHeaders["X-Custom-Header"], "Request header value should match")

    respHeaders := make(map[string]string)
    for _, h := range entry.Response.Headers {
        respHeaders[h.Name] = h.Value
    }
    assert.Equal(t, "test-value", respHeaders["X-Test-Header"], "Response header value should match")
}

func TestHarLoggerSaveAndClear(t *testing.T) {
    logger := NewLogger()

    background := httptest.NewServer(ConstantHandler("test"))
    defer background.Close()

    proxyServer := createTestProxy(logger)
    defer proxyServer.Close()

    client := createProxyClient(proxyServer.URL)

    resp, err := client.Get(background.URL)
    require.NoError(t, err, "Should send request")
    resp.Body.Close()

    time.Sleep(200 * time.Millisecond)

    entries := logger.GetEntries()
    require.Len(t, entries, 1, "Should have one log entry")

    // Save to file
    tmpDir := t.TempDir()
    harFilePath := filepath.Join(tmpDir, "test.har")
    err = logger.SaveToFile(harFilePath)
    require.NoError(t, err, "Should save HAR file")

    // Verify file contents
    harData, err := os.ReadFile(harFilePath)
    require.NoError(t, err, "Should read HAR file")

    var har Har
    err = json.Unmarshal(harData, &har)
    require.NoError(t, err, "Should parse HAR JSON")
    assert.Len(t, har.Log.Entries, 1, "Saved HAR should have one entry")
    assert.Equal(t, "1.2", har.Log.Version, "HAR version should be 1.2")

    // Clear logger
    logger.Clear()
    entries = logger.GetEntries()
    assert.Empty(t, entries, "Should have no entries after clear")
}

func TestHarLoggerConcurrency(t *testing.T) {
    logger := NewLogger()

    background := httptest.NewServer(ConstantHandler("concurrent"))
    defer background.Close()

    proxyServer := createTestProxy(logger)
    defer proxyServer.Close()

    client := createProxyClient(proxyServer.URL)

    requestCount := 50
    successChan := make(chan bool, requestCount)

    for i := 0; i < requestCount; i++ {
        go func() {
            resp, err := client.Get(background.URL)
            if err != nil {
                successChan <- false
                return
            }
            resp.Body.Close()
            successChan <- true
        }()
    }

    successCount := 0
    for i := 0; i < requestCount; i++ {
        if <-successChan {
            successCount++
        }
    }

    time.Sleep(500 * time.Millisecond)

    entries := logger.GetEntries()
    assert.Equal(t, successCount, len(entries), "Should log all successful requests")
}
