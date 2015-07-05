package main

import (
	"net/http"
	"io"
	"bytes"
	"github.com/rainycape/vfs"
	"fmt"
	"os"
	"path"
)

type CacheWriteCloser struct {
	fs   			vfs.VFS
	body 			vfs.WFile
	key 			string
	statusCode 		int
	contentLength 	int64
	headers 		http.Header
}

func NewCacheWriteCloser(fs vfs.VFS, key string, statusCode int, contentLength int64, headers http.Header) (*CacheWriteCloser, error) {
	filePath := bodyPrefix+formatPrefix+hashKey(key)
	
	body, err := vfsCreateWrite(fs, filePath)
	if err != nil {
		return nil, err
	}
	
	return &CacheWriteCloser{fs, body, key, statusCode, contentLength, headers}, nil
}

func (cacheWriteCloser *CacheWriteCloser) Write(buffer []byte) (int, error) {	
	return cacheWriteCloser.body.Write(buffer)	
}

func (cacheWriteCloser *CacheWriteCloser) Close() error {	
	if err := cacheWriteCloser.body.Close(); err != nil {
		return err
	}

	return StoreHeader(cacheWriteCloser.fs, cacheWriteCloser.statusCode, cacheWriteCloser.headers, cacheWriteCloser.key)
}

func StoreHeader(fs vfs.VFS, code int, headers http.Header, key string) error {
	headerBuffer, err := getHeaderBuffer(code, headers)
	
	if err != nil {
		return err
	}
	
	filePath := headerPrefix+formatPrefix+hashKey(key)
	
	if err := vfsWrite(fs, filePath, bytes.NewReader(headerBuffer.Bytes())); err != nil {
		return err
	}
	return nil
}

func getHeaderBuffer(code int, headers http.Header) (*bytes.Buffer, error) {
	headerBuffer := &bytes.Buffer{}
	if _, err := headerBuffer.Write([]byte(fmt.Sprintf("HTTP/1.1 %d %s\r\n", code, http.StatusText(code)))); err != nil {
		return nil, err
	}

	//Write headers
	if err := headers.Write(headerBuffer); err != nil {
		return nil, err
	}
	// ReadMIMEHeader expects a trailing newline
	if _, err := headerBuffer.Write([]byte("\r\n")); err != nil {
		return nil, err
	}
	
	return headerBuffer, nil
}

func vfsCreateWrite(fs vfs.VFS, filePath string) (vfs.WFile, error) {
	if err := vfs.MkdirAll(fs, path.Dir(filePath), 0700); err != nil {
		return nil, err
	}
	
	return fs.OpenFile(filePath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
}

func vfsWrite(fs vfs.VFS, filePath string, reader io.Reader) error {
	writer, err := vfsCreateWrite(fs, filePath)
	if err != nil {
		return err
	}
	defer writer.Close()
	if _, err := io.Copy(writer, reader); err != nil {
		return err
	}
	return nil
}
