package http1parser

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/textproto"
)

type RequestReader struct {
	preventCanonicalization bool
	reader                  *bufio.Reader
	// Used only when preventCanonicalization value is true
	cloned *bytes.Buffer
}

func NewRequestReader(preventCanonicalization bool, conn io.Reader) *RequestReader {
	if !preventCanonicalization {
		return &RequestReader{
			preventCanonicalization: false,
			reader:                  bufio.NewReader(conn),
		}
	}

	var cloned bytes.Buffer
	reader := bufio.NewReader(io.TeeReader(conn, &cloned))
	return &RequestReader{
		preventCanonicalization: true,
		reader:                  reader,
		cloned:                  &cloned,
	}
}

func (r *RequestReader) IsEOF() bool {
	_, err := r.reader.Peek(1)
	return errors.Is(err, io.EOF)
}

func (r *RequestReader) Reader() *bufio.Reader {
	return r.reader
}

func (r *RequestReader) ReadRequest() (*http.Request, error) {
	if !r.preventCanonicalization {
		// Just call the HTTP library function if the preventCanonicalization
		// configuration is disabled
		return http.ReadRequest(r.reader)
	}

	req, err := http.ReadRequest(r.reader)
	if err != nil {
		return nil, err
	}

	httpData := getRequestData(r.reader, r.cloned)
	headers, _ := Http1ExtractHeaders(httpData)
	for _, headerName := range headers {
		canonicalizedName := textproto.CanonicalMIMEHeaderKey(headerName)
		if canonicalizedName == headerName {
			continue
		}

		// Rewrite header keys to the non-canonical parsed value
		values, ok := req.Header[canonicalizedName]
		if ok {
			req.Header.Del(canonicalizedName)
			req.Header[headerName] = values
		}
	}

	return req, nil
}

func getRequestData(r *bufio.Reader, cloned *bytes.Buffer) []byte {
	// "Cloned" buffer uses the raw connection as the data source.
	// However, the *bufio.Reader can read also bytes of another unrelated
	// request on the same connection, since it's buffered, so we have to
	// ignore them before passing the data to our headers parser.
	// Data related to the next request will remain inside the buffer for
	// later usage.
	return cloned.Next(cloned.Len() - r.Buffered())
}
