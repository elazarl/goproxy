package goproxy

import (
	"compress/gzip"
	"io"
	"net/http"

	"github.com/andybalholm/brotli"
)

// decompressResponse transparently decompresses the response body based on
// Content-Encoding when the proxy itself injected the Accept-Encoding header.
// This mirrors what net/http.Transport does for gzip, extended to also cover
// brotli (br).
//
// The function is a no-op when:
//   - the client set its own Accept-Encoding (KeepAcceptEncoding=true)
//   - the response has no body (HEAD, 204, 304)
//   - the Content-Encoding is not one we handle
func decompressResponse(resp *http.Response, req *http.Request) {
	if resp == nil || resp.Body == nil {
		return
	}

	// Only decompress if there's a body to read.
	if req.Method == http.MethodHead || resp.ContentLength == 0 {
		return
	}

	switch resp.Header.Get("Content-Encoding") {
	case "br":
		resp.Header.Del("Content-Encoding")
		resp.Header.Del("Content-Length")
		resp.ContentLength = -1
		resp.Body = &readCloseWrapper{
			Reader: brotli.NewReader(resp.Body),
			Closer: resp.Body,
		}
		resp.Uncompressed = true

	case "gzip":
		// net/http.Transport usually handles gzip itself, but when the
		// proxy explicitly sets Accept-Encoding (e.g. "gzip, br"), the
		// transport sees a user-set header and skips its own
		// decompression. Handle it here as a safety net.
		gr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return // leave body as-is on error
		}
		resp.Header.Del("Content-Encoding")
		resp.Header.Del("Content-Length")
		resp.ContentLength = -1
		resp.Body = &readCloseWrapper{
			Reader: gr,
			Closer: resp.Body,
		}
		resp.Uncompressed = true
	}
}

// readCloseWrapper combines an io.Reader (the decompressor) with the
// underlying body's Close method so that closing the wrapper drains
// and closes the original transport body.
type readCloseWrapper struct {
	io.Reader
	Closer io.Closer
}

func (r *readCloseWrapper) Close() error {
	return r.Closer.Close()
}
