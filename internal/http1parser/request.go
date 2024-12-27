package http1parser

import (
	"bufio"
	"bytes"
	"net/http"
	"net/textproto"
)

func ReadRequest(preventCanonicalization bool, r *bufio.Reader, cloned *bytes.Buffer) (*http.Request, error) {
	if !preventCanonicalization {
		// Just call the HTTP library function if the preventCanonicalization
		// configuration is disabled
		req, err := http.ReadRequest(r)
		if err != nil {
			return nil, err
		}

		// Discard the raw bytes related to the current request, we don't care
		// about them since we don't have to do anything.
		// We call the function to consume the buffer.
		_ = getRequestData(r, cloned)
		return req, nil
	}

	req, err := http.ReadRequest(r)
	if err != nil {
		return nil, err
	}

	httpData := getRequestData(r, cloned)
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
