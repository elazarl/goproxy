package http1parser_test

import (
	"testing"

	"github.com/elazarl/goproxy/internal/http1parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHttp1ExtractHeaders_Empty(t *testing.T) {
	http1Data := "POST /index.html HTTP/1.1\r\n" +
		"\r\n"
	headers, err := http1parser.Http1ExtractHeaders([]byte(http1Data))
	require.NoError(t, err)
	assert.Empty(t, headers)
}

func TestHttp1ExtractHeaders(t *testing.T) {
	http1Data := "POST /index.html HTTP/1.1\r\n" +
		"Host: www.test.com\r\n" +
		"Accept: */*\r\n" +
		"Content-Length: 17\r\n" +
		"lowercase: 3z\r\n" +
		"\r\n" +
		`{"hello":"world"}`

	headers, err := http1parser.Http1ExtractHeaders([]byte(http1Data))
	require.NoError(t, err)
	assert.Len(t, headers, 4)
	assert.Contains(t, headers, "Content-Length")
	assert.Contains(t, headers, "lowercase")
}

func TestHttp1ExtractHeaders_InvalidData(t *testing.T) {
	http1Data := "POST /index.html HTTP/1.1\r\n" +
		`{"hello":"world"}`
	_, err := http1parser.Http1ExtractHeaders([]byte(http1Data))
	require.Error(t, err)
}
