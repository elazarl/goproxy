package har

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
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
            var wg sync.WaitGroup
            wg.Add(1)
            
            var exportedEntries []Entry
            exportFunc := func(entries []Entry) {
                exportedEntries = append(exportedEntries, entries...)
                wg.Done()
            }
            
            logger := NewLogger(exportFunc, WithExportThreshold(1)) // Export after each request
            defer logger.Stop()

            background := httptest.NewServer(ConstantHandler("hello world"))
            defer background.Close()

            proxyServer := createTestProxy(logger)
            defer proxyServer.Close()

            client := createProxyClient(proxyServer.URL)

            req, err := http.NewRequestWithContext(
                context.Background(),
                tc.method,
                background.URL,
                strings.NewReader(tc.body),
            )
            require.NoError(t, err, "Should create request")

            if tc.contentType != "" {
                req.Header.Set("Content-Type", tc.contentType)
            }

            resp, err := client.Do(req)
            require.NoError(t, err, "Should send request successfully")
            defer resp.Body.Close()
            
            bodyBytes, err := io.ReadAll(resp.Body)
            require.NoError(t, err, "Should read response body")
            
            body := string(bodyBytes)
            assert.Equal(t, "hello world", body, "Response body should match")

            wg.Wait() // Wait for export to complete

            assert.Len(t, exportedEntries, 1, "Should have exactly one exported entry")
            assert.Equal(t, tc.expectedMethod, exportedEntries[0].Request.Method, "Request method should match")
        })
    }
}

func TestLoggerThresholdExport(t *testing.T) {
    var wg sync.WaitGroup
    var exports [][]Entry
    wg.Add(3) // Expect 3 exports (3,3,1)
    
    exportFunc := func(entries []Entry) {
        entriesCopy := make([]Entry, len(entries))
        copy(entriesCopy, entries)
        exports = append(exports, entriesCopy)
        t.Logf("Export occurred with %d entries", len(entries))
        wg.Done()
    }

    threshold := 3
    logger := NewLogger(exportFunc, WithExportThreshold(threshold))
    
    background := httptest.NewServer(ConstantHandler("test"))
    defer background.Close()

    proxyServer := createTestProxy(logger)
    defer proxyServer.Close()

    client := createProxyClient(proxyServer.URL)

    // Send 7 requests
    for i := 0; i < 7; i++ {
        resp, err := client.Get(background.URL)
        require.NoError(t, err)
        resp.Body.Close()
    }

    // Call Stop to trigger final export of remaining entries
    logger.Stop()
    wg.Wait()

    assert.Equal(t, 3, len(exports), "should have 3 export batches")
    assert.Equal(t, threshold, len(exports[0]), "first batch should have threshold size")
    assert.Equal(t, threshold, len(exports[1]), "second batch should have threshold size")
    assert.Equal(t, 1, len(exports[2]), "last batch should have remainder")
}

func TestHarLoggerExportInterval(t *testing.T) {
    var wg sync.WaitGroup
    var exports [][]Entry
    wg.Add(1) // Expect 1 export with all entries
    
    exportFunc := func(entries []Entry) {
        entriesCopy := make([]Entry, len(entries))
        copy(entriesCopy, entries)
        exports = append(exports, entriesCopy)
        t.Logf("Export occurred with %d entries", len(entries))
        wg.Done()
    }

    logger := NewLogger(exportFunc, WithExportInterval(100*time.Millisecond))
    
    background := httptest.NewServer(ConstantHandler("test"))
    defer background.Close()

    proxyServer := createTestProxy(logger)
    defer proxyServer.Close()

    client := createProxyClient(proxyServer.URL)

    // Send 3 requests
    for i := 0; i < 3; i++ {
        resp, err := client.Get(background.URL)
        require.NoError(t, err)
        resp.Body.Close()
    }

    wg.Wait()
    logger.Stop()

    require.Equal(t, 1, len(exports), "should have 1 export batch")
    assert.Equal(t, 3, len(exports[0]), "Should have exported 3 entries")
}

