package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"strconv"
	"time"
	"github.com/lox/httpcache"
	"log"
)

const (
	lastModDivisor = 10
	viaPseudonym   = "httpcache"
)

var Clock = func() time.Time {
	return time.Now().UTC()
}

type ReadCloser interface {
	io.Reader
	io.Closer
}

type byteReadCloser struct {
	*bytes.Reader
}

func (brsc *byteReadCloser) Close() error { return nil }

type ForwardOnlyResource struct {
	ReadCloser
	RequestTime, ResponseTime time.Time
	header                    http.Header
	statusCode                int
	cc                        httpcache.CacheControl
	stale                     bool
}

func NewForwardOnlyResource(statusCode int, body ReadCloser, hdrs http.Header) *ForwardOnlyResource {
	return &ForwardOnlyResource{
		header:         hdrs,
		ReadCloser: body,
		statusCode:     statusCode,
	}
}

func NewForwardOnlyResourceBytes(statusCode int, b []byte, hdrs http.Header) *ForwardOnlyResource {
	return &ForwardOnlyResource{
		header:         hdrs,
		statusCode:     statusCode,
		ReadCloser: &byteReadCloser{bytes.NewReader(b)},
	}
}

func (r *ForwardOnlyResource) IsNonErrorStatus() bool {
	return r.statusCode >= 200 && r.statusCode < 400
}

func (r *ForwardOnlyResource) Status() int {
	return r.statusCode
}

func (r *ForwardOnlyResource) Header() http.Header {
	return r.header
}

func (r *ForwardOnlyResource) IsStale() bool {
	return r.stale
}

func (r *ForwardOnlyResource) MarkStale() {
	r.stale = true
}

func (r *ForwardOnlyResource) cacheControl() (httpcache.CacheControl, error) {
	if r.cc != nil {
		return r.cc, nil
	}

	cc, err := httpcache.ParseCacheControlHeaders(r.header)
	if err != nil {
		return cc, err
	}

	r.cc = cc
	return cc, nil
}

func (r *ForwardOnlyResource) LastModified() time.Time {
	var modTime time.Time

	if lastModHeader := r.header.Get("Last-Modified"); lastModHeader != "" {
		if t, err := http.ParseTime(lastModHeader); err == nil {
			modTime = t
		}
	}

	return modTime
}

func (r *ForwardOnlyResource) Expires() (time.Time, error) {
	if expires := r.header.Get("Expires"); expires != "" {
		return http.ParseTime(expires)
	}

	return time.Time{}, nil
}

func (r *ForwardOnlyResource) MustValidate(shared bool) bool {
	cc, err := r.cacheControl()
	if err != nil {
		log.Printf("Error parsing Cache-Control: ", err.Error())
		return true
	}

	// The s-maxage directive also implies the semantics of proxy-revalidate
	if cc.Has("s-maxage") && shared {
		return true
	}

	if cc.Has("must-revalidate") || (cc.Has("proxy-revalidate") && shared) {
		return true
	}

	return false
}

func (r *ForwardOnlyResource) DateAfter(d time.Time) bool {
	if dateHeader := r.header.Get("Date"); dateHeader != "" {
		if t, err := http.ParseTime(dateHeader); err != nil {
			return false
		} else {
			return t.After(d)
		}
	}
	return false
}

// Calculate the age of the resource
func (r *ForwardOnlyResource) Age() (time.Duration, error) {
	var age time.Duration

	if ageInt, err := intHeader("Age", r.header); err == nil {
		age = time.Second * time.Duration(ageInt)
	}

	if proxyDate, err := timeHeader(ProxyDateHeader, r.header); err == nil {
		return Clock().Sub(proxyDate) + age, nil
	}

	if date, err := timeHeader("Date", r.header); err == nil {
		return Clock().Sub(date) + age, nil
	}

	return time.Duration(0), errors.New("Unable to calculate age")
}

func (r *ForwardOnlyResource) MaxAge(shared bool) (time.Duration, error) {
	cc, err := r.cacheControl()
	if err != nil {
		return time.Duration(0), err
	}

	if cc.Has("s-maxage") && shared {
		if maxAge, err := cc.Duration("s-maxage"); err != nil {
			return time.Duration(0), err
		} else if maxAge > 0 {
			return maxAge, nil
		}
	}

	if cc.Has("max-age") {
		if maxAge, err := cc.Duration("max-age"); err != nil {
			return time.Duration(0), err
		} else if maxAge > 0 {
			return maxAge, nil
		}
	}

	if expiresVal := r.header.Get("Expires"); expiresVal != "" {
		expires, err := http.ParseTime(expiresVal)
		if err != nil {
			return time.Duration(0), err
		}
		return expires.Sub(Clock()), nil
	}

	return time.Duration(0), nil
}

func (r *ForwardOnlyResource) RemovePrivateHeaders() {
	cc, err := r.cacheControl()
	if err != nil {
		log.Printf("Error parsing Cache-Control: %s", err.Error())
	}

	for _, p := range cc["private"] {
		log.Printf("removing private header %q", p)
		r.header.Del(p)
	}
}

func (r *ForwardOnlyResource) HasValidators() bool {
	if r.header.Get("Last-Modified") != "" || r.header.Get("Etag") != "" {
		return true
	}

	return false
}

func (r *ForwardOnlyResource) HasExplicitExpiration() bool {
	cc, err := r.cacheControl()
	if err != nil {
		log.Printf("Error parsing Cache-Control: %s", err.Error())
		return false
	}

	if d, _ := cc.Duration("max-age"); d > time.Duration(0) {
		return true
	}

	if d, _ := cc.Duration("s-maxage"); d > time.Duration(0) {
		return true
	}

	if exp, _ := r.Expires(); !exp.IsZero() {
		return true
	}

	return false
}

func (r *ForwardOnlyResource) HeuristicFreshness() time.Duration {
	if !r.HasExplicitExpiration() && r.header.Get("Last-Modified") != "" {
		return Clock().Sub(r.LastModified()) / time.Duration(lastModDivisor)
	}

	return time.Duration(0)
}

func (r *ForwardOnlyResource) Via() string {
	via := []string{}
	via = append(via, fmt.Sprintf("1.1 %s", viaPseudonym))
	return strings.Join(via, ",")
}

var errNoHeader = errors.New("Header doesn't exist")

func timeHeader(key string, h http.Header) (time.Time, error) {
	if header := h.Get(key); header != "" {
		return http.ParseTime(header)
	} else {
		return time.Time{}, errNoHeader
	}
}

func intHeader(key string, h http.Header) (int, error) {
	if header := h.Get(key); header != "" {
		return strconv.Atoi(header)
	} else {
		return 0, errNoHeader
	}
}