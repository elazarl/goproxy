package goproxy

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"testing"

	"github.com/andybalholm/brotli"
)

func TestDecompressResponse_Brotli(t *testing.T) {
	original := "Hello, brotli-compressed world!"

	// Compress with brotli
	var buf bytes.Buffer
	bw := brotli.NewWriter(&buf)
	bw.Write([]byte(original))
	bw.Close()

	resp := &http.Response{
		StatusCode:    200,
		Header:        http.Header{"Content-Encoding": {"br"}},
		Body:          io.NopCloser(&buf),
		ContentLength: int64(buf.Len()),
	}
	req, _ := http.NewRequest("GET", "http://example.com", nil)

	decompressResponse(resp, req)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != original {
		t.Errorf("expected %q, got %q", original, string(body))
	}
	if resp.Header.Get("Content-Encoding") != "" {
		t.Error("Content-Encoding header should be removed")
	}
	if resp.ContentLength != -1 {
		t.Errorf("ContentLength should be -1, got %d", resp.ContentLength)
	}
	if !resp.Uncompressed {
		t.Error("Uncompressed should be true")
	}
}

func TestDecompressResponse_Gzip(t *testing.T) {
	original := "Hello, gzip-compressed world!"

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	gw.Write([]byte(original))
	gw.Close()

	resp := &http.Response{
		StatusCode:    200,
		Header:        http.Header{"Content-Encoding": {"gzip"}},
		Body:          io.NopCloser(&buf),
		ContentLength: int64(buf.Len()),
	}
	req, _ := http.NewRequest("GET", "http://example.com", nil)

	decompressResponse(resp, req)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != original {
		t.Errorf("expected %q, got %q", original, string(body))
	}
	if resp.Header.Get("Content-Encoding") != "" {
		t.Error("Content-Encoding header should be removed")
	}
	if !resp.Uncompressed {
		t.Error("Uncompressed should be true")
	}
}

func TestDecompressResponse_Identity(t *testing.T) {
	original := "Hello, uncompressed world!"

	resp := &http.Response{
		StatusCode:    200,
		Header:        http.Header{},
		Body:          io.NopCloser(bytes.NewBufferString(original)),
		ContentLength: int64(len(original)),
	}
	req, _ := http.NewRequest("GET", "http://example.com", nil)

	decompressResponse(resp, req)

	body, _ := io.ReadAll(resp.Body)
	if string(body) != original {
		t.Errorf("expected %q, got %q", original, string(body))
	}
	if resp.ContentLength != int64(len(original)) {
		t.Error("ContentLength should not be modified for uncompressed response")
	}
}

func TestDecompressResponse_HeadRequest(t *testing.T) {
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Encoding": {"br"}},
		Body:       io.NopCloser(bytes.NewBuffer(nil)),
	}
	req, _ := http.NewRequest("HEAD", "http://example.com", nil)

	// Should be a no-op for HEAD requests
	decompressResponse(resp, req)

	if resp.Header.Get("Content-Encoding") != "br" {
		t.Error("Content-Encoding should not be modified for HEAD requests")
	}
}

func TestDecompressResponse_NilResponse(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	// Should not panic
	decompressResponse(nil, req)
}
