package main

import (
	"io"
	"bytes"
)

type ReadCloser interface {
	io.Reader
	io.Closer
}

type byteReadCloser struct {
	*bytes.Reader
}

func (brsc *byteReadCloser) Close() error { return nil }