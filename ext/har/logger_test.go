package har_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elazarl/goproxy"
	"github.com/elazarl/goproxy/ext/har"
	"github.com/elazarl/goproxy/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newLoggingProxyClient returns a client routed through a proxy that reports
// every request and response to logger.
func newLoggingProxyClient(t *testing.T, logger *har.Logger) *http.Client {
	t.Helper()
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().DoFunc(logger.OnRequest)
	proxy.OnResponse().DoFunc(logger.OnResponse)
	client, _ := testutil.NewProxy(t, proxy)
	return client
}

func TestLoggerRecordsRequestMethod(t *testing.T) {
	testCases := []struct {
		name        string
		method      string
		body        string
		contentType string
	}{
		{
			name:   "GET request",
			method: http.MethodGet,
		},
		{
			name:        "POST request",
			method:      http.MethodPost,
			body:        `{"test":"data"}`,
			contentType: "application/json",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var exported []har.Entry
			var wg sync.WaitGroup
			wg.Add(1)

			// A threshold of one exports after every single request.
			logger := har.NewLogger(func(entries []har.Entry) {
				exported = append(exported, entries...)
				wg.Done()
			}, har.WithExportThreshold(1))
			t.Cleanup(logger.Stop)

			background := httptest.NewServer(testutil.ConstantHandler("hello world"))
			t.Cleanup(background.Close)
			client := newLoggingProxyClient(t, logger)

			req, err := http.NewRequestWithContext(
				context.Background(),
				tc.method,
				background.URL,
				strings.NewReader(tc.body),
			)
			require.NoError(t, err)
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}

			resp, err := client.Do(req)
			require.NoError(t, err)
			t.Cleanup(func() { _ = resp.Body.Close() })

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Equal(t, "hello world", string(body))

			wg.Wait() // the logger exports asynchronously

			require.Len(t, exported, 1)
			assert.Equal(t, tc.method, exported[0].Request.Method)
		})
	}
}

func TestLoggerExportsOnThreshold(t *testing.T) {
	const (
		threshold = 3
		requests  = 7
	)

	var mtx sync.Mutex
	var exports [][]har.Entry
	var wg sync.WaitGroup
	wg.Add(3) // two full batches, plus the leftover entry flushed by Stop

	logger := har.NewLogger(func(entries []har.Entry) {
		mtx.Lock()
		exports = append(exports, entries)
		mtx.Unlock()
		wg.Done()
	}, har.WithExportThreshold(threshold))

	background := httptest.NewServer(testutil.ConstantHandler("test"))
	t.Cleanup(background.Close)
	client := newLoggingProxyClient(t, logger)

	for range requests {
		testutil.GetOrFail(t, client, background.URL)
	}

	logger.Stop() // exports the entries that did not reach the threshold
	wg.Wait()

	mtx.Lock()
	defer mtx.Unlock()
	require.Len(t, exports, 3, "should have 3 export batches")

	batchCounts := make(map[int]int)
	for _, batch := range exports {
		batchCounts[len(batch)]++
	}
	assert.Equal(t, 2, batchCounts[threshold], "should have two batches of threshold size")
	assert.Equal(t, 1, batchCounts[1], "should have one batch with the single remaining entry")
}

func TestLoggerExportsOnInterval(t *testing.T) {
	const requests = 3

	var mtx sync.Mutex
	var exports [][]har.Entry
	var wg sync.WaitGroup
	wg.Add(1) // expect a single export holding every entry

	logger := har.NewLogger(func(entries []har.Entry) {
		mtx.Lock()
		exports = append(exports, entries)
		mtx.Unlock()
		wg.Done()
	}, har.WithExportInterval(time.Second))
	t.Cleanup(logger.Stop)

	background := httptest.NewServer(testutil.ConstantHandler("test"))
	t.Cleanup(background.Close)
	client := newLoggingProxyClient(t, logger)

	for range requests {
		testutil.GetOrFail(t, client, background.URL)
	}

	wg.Wait() // the interval has to elapse before the export happens

	mtx.Lock()
	defer mtx.Unlock()
	require.Len(t, exports, 1, "should have 1 export batch")
	assert.Len(t, exports[0], requests)
}
