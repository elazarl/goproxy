package http1parser_test

import (
	"bufio"
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

	var cloned bytes.Buffer
	r := bufio.NewReader(io.TeeReader(http1Data, &cloned))

	// 1st request
	req, err := http1parser.ReadRequest(false, r, &cloned)
	require.NoError(t, err)
	assert.NotEmpty(t, req.Header)
	assert.NotContains(t, req.Header, "lowercase")
	assert.Contains(t, req.Header, "Lowercase")

	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	assert.Len(t, body, 17)
	require.NoError(t, req.Body.Close())

	// 2nd request
	req, err = http1parser.ReadRequest(false, r, &cloned)
	require.NoError(t, err)
	assert.NotEmpty(t, req.Header)

	// Make sure that the buffers are empty after all requests have been processed
	assert.Equal(t, 0, r.Buffered())
	assert.Equal(t, 0, cloned.Len())
}

func TestNonCanonicalRequest(t *testing.T) {
	http1Data := bytes.NewReader([]byte(_data))

	var cloned bytes.Buffer
	r := bufio.NewReader(io.TeeReader(http1Data, &cloned))

	req, err := http1parser.ReadRequest(true, r, &cloned)
	require.NoError(t, err)
	assert.NotEmpty(t, req.Header)
	assert.Contains(t, req.Header, "lowercase")
	assert.NotContains(t, req.Header, "Lowercase")
}

func TestMultipleNonCanonicalRequests(t *testing.T) {
	http1Data := bytes.NewReader(append([]byte(_data), _data2...))

	var cloned bytes.Buffer
	r := bufio.NewReader(io.TeeReader(http1Data, &cloned))

	req, err := http1parser.ReadRequest(true, r, &cloned)
	require.NoError(t, err)
	assert.NotEmpty(t, req.Header)
	assert.Contains(t, req.Header, "lowercase")
	assert.NotContains(t, req.Header, "Lowercase")

	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	assert.Len(t, body, 17)
	require.NoError(t, req.Body.Close())

	req, err = http1parser.ReadRequest(true, r, &cloned)
	require.NoError(t, err)
	assert.NotEmpty(t, req.Header)

	assert.Equal(t, 0, cloned.Len())
	assert.Equal(t, 0, r.Buffered())
}
