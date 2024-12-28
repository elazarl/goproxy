package http1parser_test

import (
	"bytes"
	"io"
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
