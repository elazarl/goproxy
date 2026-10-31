package http1parser_test

import (
	"bufio"
	"io"
	"net/textproto"
	"strings"
	"testing"

	"github.com/elazarl/goproxy/internal/http1parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTextReader(data string) *textproto.Reader {
	return textproto.NewReader(bufio.NewReader(strings.NewReader(data)))
}

func TestHttp1ExtractHeaders_Empty(t *testing.T) {
	http1Data := "POST /index.html HTTP/1.1\r\n" +
		"\r\n"

	headers, err := http1parser.Http1ExtractHeaders(newTextReader(http1Data))
	require.NoError(t, err)
	assert.Empty(t, headers)
}

func TestHttp1ExtractHeaders(t *testing.T) {
	http1Data := "POST /index.html HTTP/1.1\r\n" +
		"Host: www.test.com\r\n" +
		"Accept: */ /*\r\n" +
		"Content-Length: 17\r\n" +
		"lowercase: 3z\r\n" +
		"\r\n" +
		`{"hello":"world"}`

	headers, err := http1parser.Http1ExtractHeaders(newTextReader(http1Data))
	require.NoError(t, err)
	// Header names are returned as they were received, without canonicalization.
	assert.Equal(t, []string{"Host", "Accept", "Content-Length", "lowercase"}, headers)
}

func TestHttp1ExtractHeaders_InvalidData(t *testing.T) {
	http1Data := "POST /index.html HTTP/1.1\r\n" +
		`{"hello":"world"}`

	_, err := http1parser.Http1ExtractHeaders(newTextReader(http1Data))
	// The header block is never terminated, so the reader hits the end of the data.
	require.ErrorIs(t, err, io.EOF)
}
