package http1parser_test

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/elazarl/goproxy/internal/http1parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	_data = "POST /index.html HTTP/1.1\r\n" +
		"Host: www.test.com\r\n" +
		"Accept: */*\r\n" +
		"Content-Length: 17\r\n" +
		"lowercase: 3z\r\n" +
		"\r\n" +
		`{"hello":"world"}`

	_data2 = "GET /index.html HTTP/1.1\r\n" +
		"Host: www.test.com\r\n" +
		"Accept: */*\r\n" +
		"lowercase: 3z\r\n" +
		"\r\n"
)

func TestCanonicalRequest(t *testing.T) {
	// Here we are simulating two requests on the same connection
	http1Data := bytes.NewReader(append([]byte(_data), _data2...))
	parser := http1parser.NewRequestReader(false, http1Data)

	// 1st request
	req, err := parser.ReadRequest()
	require.NoError(t, err)
	assert.NotEmpty(t, req.Header)
	assert.NotContains(t, req.Header, "lowercase")
	assert.Contains(t, req.Header, "Lowercase")
	require.NoError(t, req.Body.Close())

	// 2nd request
	req, err = parser.ReadRequest()
	require.NoError(t, err)
	assert.NotEmpty(t, req.Header)

	// Make sure that the buffers are empty after all requests have been processed
	assert.True(t, parser.IsEOF())
}

func TestNonCanonicalRequest(t *testing.T) {
	http1Data := bytes.NewReader([]byte(_data))
	parser := http1parser.NewRequestReader(true, http1Data)

	req, err := parser.ReadRequest()
	require.NoError(t, err)
	assert.NotEmpty(t, req.Header)
	assert.Contains(t, req.Header, "lowercase")
	assert.NotContains(t, req.Header, "Lowercase")
}

func TestMultipleNonCanonicalRequests(t *testing.T) {
	http1Data := bytes.NewReader(append([]byte(_data), _data2...))
	parser := http1parser.NewRequestReader(true, http1Data)

	req, err := parser.ReadRequest()
	require.NoError(t, err)
	assert.NotEmpty(t, req.Header)
	assert.Contains(t, req.Header, "lowercase")
	assert.NotContains(t, req.Header, "Lowercase")

	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	assert.Len(t, body, 17)
	require.NoError(t, req.Body.Close())

	req, err = parser.ReadRequest()
	require.NoError(t, err)
	assert.NotEmpty(t, req.Header)

	assert.True(t, parser.IsEOF())
}

// reqTest is inspired by https://github.com/golang/go/blob/master/src/net/http/readrequest_test.go
type reqTest struct {
	Raw     string
	Req     *http.Request
	Body    string
	Trailer http.Header
	Error   string
}

var (
	noError   = ""
	noBodyStr = ""
	noTrailer http.Header
)

var reqTests = []reqTest{
	// Baseline test; All Request fields included for template use
	{
		"GET http://www.techcrunch.com/ HTTP/1.1\r\n" +
			"Host: www.techcrunch.com\r\n" +
			"user-agent: Fake\r\n" +
			"Accept: text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8\r\n" +
			"Accept-Language: en-us,en;q=0.5\r\n" +
			"Accept-Encoding: gzip,deflate\r\n" +
			"Accept-Charset: ISO-8859-1,utf-8;q=0.7,*;q=0.7\r\n" +
			"Keep-Alive: 300\r\n" +
			"Content-Length: 7\r\n" +
			"Proxy-Connection: keep-alive\r\n\r\n" +
			"abcdef\n???",
		&http.Request{
			Method: http.MethodGet,
			URL: &url.URL{
				Scheme: "http",
				Host:   "www.techcrunch.com",
				Path:   "/",
			},
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Header: http.Header{
				"Accept":           {"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"},
				"Accept-Language":  {"en-us,en;q=0.5"},
				"Accept-Encoding":  {"gzip,deflate"},
				"Accept-Charset":   {"ISO-8859-1,utf-8;q=0.7,*;q=0.7"},
				"Keep-Alive":       {"300"},
				"Proxy-Connection": {"keep-alive"},
				"Content-Length":   {"7"},
				"user-agent":       {"Fake"},
			},
			Close:         false,
			ContentLength: 7,
			Host:          "www.techcrunch.com",
			RequestURI:    "http://www.techcrunch.com/",
		},
		"abcdef\n",
		noTrailer,
		noError,
	},

	// GET request with no body (the normal case)
	{
		"GET / HTTP/1.1\r\n" +
			"Host: foo.com\r\n\r\n",
		&http.Request{
			Method: http.MethodGet,
			URL: &url.URL{
				Path: "/",
			},
			Proto:         "HTTP/1.1",
			ProtoMajor:    1,
			ProtoMinor:    1,
			Header:        http.Header{},
			Close:         false,
			ContentLength: 0,
			Host:          "foo.com",
			RequestURI:    "/",
		},
		noBodyStr,
		noTrailer,
		noError,
	},
}

func TestReadRequest(t *testing.T) {
	for i := range reqTests {
		tt := &reqTests[i]

		testName := fmt.Sprintf("Test %d (%q)", i, tt.Raw)
		t.Run(testName, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(tt.Raw))
			parser := http1parser.NewRequestReader(true, r)
			req, err := parser.ReadRequest()
			if err != nil && err.Error() == tt.Error {
				// Test finished, we expected an error
				return
			}
			require.NoError(t, err)

			// Check request equality (excluding body)
			rbody := req.Body
			req.Body = nil
			assert.Equal(t, tt.Req, req)

			// Check if the two bodies match
			var bodyString string
			if rbody != nil {
				data, err := io.ReadAll(rbody)
				require.NoError(t, err)
				bodyString = string(data)
				_ = rbody.Close()
			}
			assert.Equal(t, tt.Body, bodyString)
			assert.Equal(t, tt.Trailer, req.Trailer)
		})
	}
}
