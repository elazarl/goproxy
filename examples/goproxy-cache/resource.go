package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	lastModDivisor = 10
	viaPseudonym   = "httpcache"
)


type Resource struct {
	ReadCloser
	RequestTime, ResponseTime time.Time
	header                    http.Header
	contentLength			  int64
	statusCode                int
	cc                        CacheControl
	stale                     bool
}

func NewResource(statusCode int, contentLength int64, body ReadCloser, hdrs http.Header) *Resource {
	return &Resource{
		header:         hdrs,
		ReadCloser: body,
		statusCode:     statusCode,
		contentLength:  contentLength,
	}
}

func NewResourceBytes(statusCode int, contentLength int64, b []byte, hdrs http.Header) *Resource {
	return &Resource{
		header:         hdrs,
		statusCode:     statusCode,
		contentLength:  contentLength,
		ReadCloser: &byteReadCloser{bytes.NewReader(b)},
	}
}

func (r *Resource) IsNonErrorStatus() bool {
	return r.statusCode >= 200 && r.statusCode < 400
}

func (r *Resource) Status() int {
	return r.statusCode
}

func (r *Resource) ContentLength() int64 {
	return r.contentLength
}

func (r *Resource) Header() http.Header {
	return r.header
}

func (r *Resource) IsStale() bool {
	return r.stale
}

func (r *Resource) MarkStale() {
	r.stale = true
}

func (r *Resource) cacheControl() (CacheControl, error) {
	if r.cc != nil {
		return r.cc, nil
	}

	cc, err := ParseCacheControlHeaders(r.header)
	if err != nil {
		return cc, err
	}

	r.cc = cc
	return cc, nil
}

func (r *Resource) LastModified() time.Time {
	var modTime time.Time

	if lastModHeader := r.header.Get("Last-Modified"); lastModHeader != "" {
		if t, err := http.ParseTime(lastModHeader); err == nil {
			modTime = t
		}
	}

	return modTime
}

func (r *Resource) Expires() (time.Time, error) {
	if expires := r.header.Get("Expires"); expires != "" {
		return http.ParseTime(expires)
	}

	return time.Time{}, nil
}

func (r *Resource) MustValidate(shared bool) bool {
	cc, err := r.cacheControl()
	if err != nil {
		debugf("Error parsing Cache-Control: ", err.Error())
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

func (r *Resource) DateAfter(d time.Time) bool {
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
func (r *Resource) Age() (time.Duration, error) {
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

func (r *Resource) MaxAge(shared bool) (time.Duration, error) {
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

func (r *Resource) RemovePrivateHeaders() {
	cc, err := r.cacheControl()
	if err != nil {
		debugf("Error parsing Cache-Control: %s", err.Error())
	}

	for _, p := range cc["private"] {
		debugf("removing private header %q", p)
		r.header.Del(p)
	}
}

func (r *Resource) HasValidators() bool {
	if r.header.Get("Last-Modified") != "" || r.header.Get("Etag") != "" {
		return true
	}

	return false
}

func (r *Resource) HasExplicitExpiration() bool {
	cc, err := r.cacheControl()
	if err != nil {
		debugf("Error parsing Cache-Control: %s", err.Error())
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

func (r *Resource) HeuristicFreshness() time.Duration {
	if !r.HasExplicitExpiration() && r.header.Get("Last-Modified") != "" {
		return Clock().Sub(r.LastModified()) / time.Duration(lastModDivisor)
	}

	return time.Duration(0)
}

func (r *Resource) Via() string {
	via := []string{}
	via = append(via, fmt.Sprintf("1.1 %s", viaPseudonym))
	return strings.Join(via, ",")
}
