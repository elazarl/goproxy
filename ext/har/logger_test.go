package har

import (
    "io"
    "net/http"
    "net/http/httptest"
    "net/url"
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

            var exportedEntries []Entry
            exportFunc := func(entries []Entry) {
                exportedEntries = append(exportedEntries, entries...)
            }
            logger := NewLogger(exportFunc)
            defer logger.Stop()

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

            // Verify exported entries
            assert.Len(t, exportedEntries, 0, "Should not have exported entries yet")
        })
    }
}

func TestHarLoggerExportInterval(t *testing.T) {
    var exportedEntries []Entry
    exportFunc := func(entries []Entry) {
        exportedEntries = append(exportedEntries, entries...)
    }
    logger := NewLogger(exportFunc, WithExportInterval(500*time.Millisecond))
    defer logger.Stop()

    background := httptest.NewServer(ConstantHandler("test"))
    defer background.Close()

    proxyServer := createTestProxy(logger)
    defer proxyServer.Close()

    client := createProxyClient(proxyServer.URL)

    // Send 3 requests
    for i := 0; i < 3; i++ {
        resp, err := client.Get(background.URL)
        require.NoError(t, err, "Should send request")
        resp.Body.Close()
        time.Sleep(200 * time.Millisecond)
    }

    // Wait for export interval
    time.Sleep(600 * time.Millisecond)

    assert.Len(t, exportedEntries, 3, "Should have exported 3 entries")
}

func TestHarLoggerExportCount(t *testing.T) {
    var exportedEntries []Entry
    exportFunc := func(entries []Entry) {
        exportedEntries = append(exportedEntries, entries...)
    }
    logger := NewLogger(exportFunc, WithExportCount(2))
    defer logger.Stop()

    background := httptest.NewServer(ConstantHandler("test"))
    defer background.Close()

    proxyServer := createTestProxy(logger)
    defer proxyServer.Close()

    client := createProxyClient(proxyServer.URL)

    // Send 3 requests
    for i := 0; i < 3; i++ {
        resp, err := client.Get(background.URL)
        require.NoError(t, err, "Should send request")
        resp.Body.Close()
        time.Sleep(100 * time.Millisecond)
    }

    time.Sleep(200 * time.Millisecond)

    assert.Len(t, exportedEntries, 2, "Should have exported 2 entries")
}
