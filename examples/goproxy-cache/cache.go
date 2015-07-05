package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/textproto"
	"os"
	pathutil "path"
	"strconv"
	"strings"
	"time"

	"github.com/rainycape/vfs"
)

const (
	headerPrefix = "header/"
	bodyPrefix   = "body/"
	formatPrefix = "v1/"
)

// Returned when a resource doesn't exist
var ErrNotFoundInCache = errors.New("Not found in cache")

type Cache interface {
	Header(key string) (Header, error)
	Store(res *Resource, keys ...string) error
	Retrieve(key string) (*Resource, error)
	Invalidate(keys ...string)
	Freshen(res *Resource, keys ...string) error
}

// cache provides a storage mechanism for cached Resources
type cache struct {
	fs    vfs.VFS
	stale map[string]time.Time
}

var _ Cache = (*cache)(nil)

type Header struct {
	http.Header
	StatusCode int
}

// NewCache returns a cache backend off the provided VFS
func NewVFSCache(fs vfs.VFS) Cache {
	return &cache{fs: fs, stale: map[string]time.Time{}}
}

// NewMemoryCache returns an ephemeral cache in memory
func NewMemoryCache() Cache {
	return NewVFSCache(vfs.Memory())
}

// NewDiskCache returns a disk-backed cache
func NewDiskCache(dir string) (Cache, error) {
	if err := os.MkdirAll(dir, 0777); err != nil {
		return nil, err
	}
	fs, err := vfs.FS(dir)
	if err != nil {
		return nil, err
	}
	chfs, err := vfs.Chroot("/", fs)
	if err != nil {
		return nil, err
	}
	return NewVFSCache(chfs), nil
}

func (c *cache) vfsWrite(path string, r io.Reader) error {
	if err := vfs.MkdirAll(c.fs, pathutil.Dir(path), 0700); err != nil {
		return err
	}
	f, err := c.fs.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return err
	}
	return nil
}

// Retrieve the Status and Headers for a given key path
func (c *cache) Header(key string) (Header, error) {
	path := headerPrefix + formatPrefix + hashKey(key)
	f, err := c.fs.Open(path)
	if err != nil {
		if vfs.IsNotExist(err) {
			return Header{}, ErrNotFoundInCache
		}
		return Header{}, err
	}

	return readHeaders(bufio.NewReader(f))
}

// Store a resource against a number of keys
func (c *cache) Store(res *Resource, keys ...string) error {
	var buf = &bytes.Buffer{}

	if length, err := strconv.ParseInt(res.Header().Get("Content-Length"), 10, 64); err == nil {
		if _, err = io.CopyN(buf, res, length); err != nil {
			return err
		}
	} else if _, err = io.Copy(buf, res); err != nil {
		return err
	}

	for _, key := range keys {
		delete(c.stale, key)

		if err := c.storeBody(buf, key); err != nil {
			return err
		}

		if err := c.storeHeader(res.Status(), res.Header(), key); err != nil {
			return err
		}
	}

	return nil
}

func (c *cache) storeBody(r io.Reader, key string) error {
	if err := c.vfsWrite(bodyPrefix+formatPrefix+hashKey(key), r); err != nil {
		return err
	}
	return nil
}

func (c *cache) storeHeader(code int, h http.Header, key string) error {
	hb := &bytes.Buffer{}
	hb.Write([]byte(fmt.Sprintf("HTTP/1.1 %d %s\r\n", code, http.StatusText(code))))
	headersToWriter(h, hb)

	if err := c.vfsWrite(headerPrefix+formatPrefix+hashKey(key), bytes.NewReader(hb.Bytes())); err != nil {
		return err
	}
	return nil
}

// Retrieve returns a cached Resource for the given key
func (c *cache) Retrieve(key string) (*Resource, error) {
	f, err := c.fs.Open(bodyPrefix + formatPrefix + hashKey(key))
	if err != nil {
		if vfs.IsNotExist(err) {
			return nil, ErrNotFoundInCache
		}
		return nil, err
	}
	h, err := c.Header(key)
	if err != nil {
		if vfs.IsNotExist(err) {
			return nil, ErrNotFoundInCache
		}
		return nil, err
	}
	
	contentLength, err := f.Seek(0, 2)
	if err != nil {
		if vfs.IsNotExist(err) {
			return nil, ErrNotFoundInCache
		}
		return nil, err
	}	
	f.Seek(0, 0)
	
	res := NewResource(h.StatusCode, contentLength, f, h.Header)
	if staleTime, exists := c.stale[key]; exists {
		if !res.DateAfter(staleTime) {
			log.Printf("stale marker of %s found", staleTime)
			res.MarkStale()
		}
	}
	return res, nil
}

func (c *cache) Invalidate(keys ...string) {
	log.Printf("invalidating %q", keys)
	for _, key := range keys {
		c.stale[key] = Clock()
	}
}

func (c *cache) Freshen(res *Resource, keys ...string) error {
	for _, key := range keys {
		if h, err := c.Header(key); err == nil {
			if h.StatusCode == res.Status() && headersEqual(h.Header, res.Header()) {
				debugf("freshening key %s", key)
				if err := c.storeHeader(h.StatusCode, res.Header(), key); err != nil {
					return err
				}
			} else {
				debugf("freshen failed, invalidating %s", key)
				c.Invalidate(key)
			}
		}
	}
	return nil
}

func hashKey(key string) string {
	h := sha256.New()
	io.WriteString(h, key)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func readHeaders(r *bufio.Reader) (Header, error) {
	tp := textproto.NewReader(r)
	line, err := tp.ReadLine()
	if err != nil {
		return Header{}, err
	}

	f := strings.SplitN(line, " ", 3)
	if len(f) < 2 {
		return Header{}, fmt.Errorf("malformed HTTP response: %s", line)
	}
	statusCode, err := strconv.Atoi(f[1])
	if err != nil {
		return Header{}, fmt.Errorf("malformed HTTP status code: %s", f[1])
	}

	mimeHeader, err := tp.ReadMIMEHeader()
	if err != nil {
		return Header{}, err
	}
	return Header{StatusCode: statusCode, Header: http.Header(mimeHeader)}, nil
}

func headersToWriter(h http.Header, w io.Writer) error {
	if err := h.Write(w); err != nil {
		return err
	}
	// ReadMIMEHeader expects a trailing newline
	_, err := w.Write([]byte("\r\n"))
	return err
}
