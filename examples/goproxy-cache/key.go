package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Key represents a unique identifier for a resource in the cache
type Key struct {
	method string
	header http.Header
	u      url.URL
	vary   []string
}

// NewKey returns a new Key instance
func NewKey(method string, u *url.URL, h http.Header) Key {
	return Key{method: method, header: h, u: *u, vary: []string{}}
}

// RequestKey generates a Key for a request
func NewRequestKey(r *http.Request) Key {
	URL := r.URL

	if location := r.Header.Get("Content-Location"); location != "" {
		u, err := url.Parse(location)
		if err == nil {
			if !u.IsAbs() {
				u = r.URL.ResolveReference(u)
			}
			if u.Host != r.Host {
				debugf("illegal host %q in Content-Location", u.Host)
			} else {
				debugf("using Content-Location: %q", u.String())
				URL = u
			}
		} else {
			debugf("failed to parse Content-Location %q", location)
		}
	}

	return NewKey(r.Method, URL, r.Header)
}

// ForKey returns a new Key with a given method
func (k Key) ForMethod(method string) Key {
	k2 := k
	k2.method = method
	return k2
}

// Vary returns a Key that is varied on particular headers in a http.Request
func (k Key) Vary(varyHeader string, r *http.Request) Key {
	k2 := k

	for _, header := range strings.Split(varyHeader, ", ") {
		k2.vary = append(k2.vary, header+"="+r.Header.Get(header))
	}

	return k2
}

func (k Key) String() string {
	URL := strings.ToLower(canonicalURL(&k.u).String())
	b := &bytes.Buffer{}
	b.WriteString(fmt.Sprintf("%s:%s", k.method, URL))

	if len(k.vary) > 0 {
		b.WriteString("::")
		for _, v := range k.vary {
			b.WriteString(v + ":")
		}
	}

	return b.String()
}

func canonicalURL(u *url.URL) *url.URL {
	return u
}
