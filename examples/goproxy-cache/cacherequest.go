package main

import (
	"net/http"
	"time"
	"errors"
	"github.com/lox/httpcache"
)

type cacheRequest struct {
	*http.Request
	Key          httpcache.Key
	Time         time.Time
	CacheControl httpcache.CacheControl
}

func NewCacheRequest(r *http.Request) (*cacheRequest, error) {
	cc, err := httpcache.ParseCacheControl(r.Header.Get("Cache-Control"))
	if err != nil {
		return nil, err
	}

	if r.Proto == "HTTP/1.1" && r.Host == "" {
		return nil, errors.New("Host header can't be empty")
	}

	return &cacheRequest{
		Request:      r,
		Key:          httpcache.NewRequestKey(r),
		Time:         httpcache.Clock(),
		CacheControl: cc,
	}, nil
}

func (r *cacheRequest) IsStateChanging() bool {
	if !(r.Method == "POST" || r.Method == "PUT" || r.Method == "DELETE") {
		return true
	}

	return false
}

func (r *cacheRequest) IsCacheable() bool {
	if !(r.Method == "GET" || r.Method == "HEAD") {
		return false
	}

	if r.Header.Get("If-Match") != "" ||
		r.Header.Get("If-Unmodified-Since") != "" ||
		r.Header.Get("If-Range") != "" {
		return false
	}

	if maxAge, ok := r.CacheControl.Get("max-age"); ok && maxAge == "0" {
		return false
	}

	if r.CacheControl.Has("no-store") || r.CacheControl.Has("no-cache") {
		return false
	}

	return true
}
