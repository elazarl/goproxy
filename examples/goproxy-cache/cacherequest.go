package main

import (
	"errors"
	"net/http"
	"time"
)

type cacheRequest struct {
	*http.Request
	Key          Key
	Time         time.Time
	CacheControl CacheControl
}

func NewCacheRequest(request *http.Request) (*cacheRequest, error) {
	cacheControl, err := ParseCacheControl(request.Header.Get("Cache-Control"))
	if err != nil {
		return nil, err
	}

	if request.Proto == "HTTP/1.1" && request.Host == "" {
		return nil, errors.New("Host header can't be empty")
	}

	return &cacheRequest{
		Request:      request,
		Key:          NewRequestKey(request),
		Time:         Clock(),
		CacheControl: cacheControl,
	}, nil
}

func (cacheRequest *cacheRequest) IsStateChanging() bool {
	if !(cacheRequest.Method == "POST" || cacheRequest.Method == "PUT" || cacheRequest.Method == "DELETE") {
		return true
	}

	return false
}

func (cacheRequest *cacheRequest) IsCacheable() bool {
	if !(cacheRequest.Method == "GET" || cacheRequest.Method == "HEAD") {
		return false
	}

	if cacheRequest.Header.Get("If-Match") != "" ||
		cacheRequest.Header.Get("If-Unmodified-Since") != "" ||
		cacheRequest.Header.Get("If-Range") != "" {
		return false
	}

	if maxAge, ok := cacheRequest.CacheControl.Get("max-age"); ok && maxAge == "0" {
		return false
	}

	if cacheRequest.CacheControl.Has("no-store") || cacheRequest.CacheControl.Has("no-cache") || cacheRequest.CacheControl.Has("private") {
		return false
	}

	return true
}
