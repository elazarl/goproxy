package main_test

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	httpcache "github.com/elazarl/goproxy/examples/goproxy-cache"
)

func newRequest(method, url string, h ...string) *http.Request {
	req, err := http.NewRequest(method, url, strings.NewReader(""))
	if err != nil {
		panic(err)
	}
	req.Header = parseHeaders(h)
	req.RemoteAddr = "test.local"
	return req
}

func newResponse(status int, body []byte, h ...string) *http.Response {
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		StatusCode:    status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		ContentLength: int64(len(body)),
		Body:          ioutil.NopCloser(bytes.NewReader(body)),
		Header:        parseHeaders(h),
		Close:         true,
	}
}

func parseHeaders(input []string) http.Header {
	headers := http.Header{}
	for _, header := range input {
		if idx := strings.Index(header, ": "); idx != -1 {
			headers.Add(header[0:idx], strings.TrimSpace(header[idx+1:]))
		}
	}
	return headers
}

type client struct {
	handler      http.Handler
	cacheHandler *httpcache.Handler
}

func (c *client) do(r *http.Request) *clientResponse {
	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, r)
	rec.Flush()

	var age int
	var err error

	if ageHeader := rec.HeaderMap.Get("Age"); ageHeader != "" {
		age, err = strconv.Atoi(ageHeader)
		if err != nil {
			panic("Can't parse age header")
		}
	}

	// wait for writes to finish
	httpcache.Writes.Wait()

	return &clientResponse{
		ResponseRecorder: rec,
		cacheStatus:      rec.HeaderMap.Get(httpcache.CacheHeader),
		statusCode:       rec.Code,
		age:              time.Second * time.Duration(age),
		body:             rec.Body.Bytes(),
		header:           rec.HeaderMap,
	}
}

func (c *client) get(path string, headers ...string) *clientResponse {
	return c.do(newRequest("GET", "http://example.org"+path, headers...))
}

func (c *client) head(path string, headers ...string) *clientResponse {
	return c.do(newRequest("HEAD", "http://example.org"+path, headers...))
}

func (c *client) put(path string, headers ...string) *clientResponse {
	return c.do(newRequest("PUT", "http://example.org"+path, headers...))
}

func (c *client) post(path string, headers ...string) *clientResponse {
	return c.do(newRequest("POST", "http://example.org"+path, headers...))
}

type clientResponse struct {
	*httptest.ResponseRecorder
	cacheStatus string
	statusCode  int
	age         time.Duration
	body        []byte
	header      http.Header
}

type upstreamServer struct {
	Now              time.Time
	Body             []byte
	Filename         string
	CacheControl     string
	Etag, Vary       string
	LastModified     time.Time
	ResponseDuration time.Duration
	StatusCode       int
	Header           http.Header
	asserts          []func(r *http.Request)
	requests         int
}

func (u *upstreamServer) timeTravel(d time.Duration) {
	u.Now = u.Now.Add(d)
}

func (u *upstreamServer) assert(f func(r *http.Request)) {
	u.asserts = append(u.asserts, f)
}

func (u *upstreamServer) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	u.requests = u.requests + 1

	for _, assertf := range u.asserts {
		assertf(req)
	}

	if !u.Now.IsZero() {
		rw.Header().Set("Date", u.Now.Format(http.TimeFormat))
	}

	if u.CacheControl != "" {
		rw.Header().Set("Cache-Control", u.CacheControl)
	}

	if u.Etag != "" {
		rw.Header().Set("Etag", u.Etag)
	}

	if u.Vary != "" {
		rw.Header().Set("Vary", u.Vary)
	}

	if u.Header != nil {
		for key, headers := range u.Header {
			for _, header := range headers {
				rw.Header().Add(key, header)
			}
		}
	}

	u.timeTravel(u.ResponseDuration)

	if u.StatusCode != 0 && u.StatusCode != 200 {
		rw.WriteHeader(u.StatusCode)
		io.Copy(rw, bytes.NewReader(u.Body))
	} else {
		http.ServeContent(rw, req, u.Filename, u.LastModified, bytes.NewReader(u.Body))
	}
}

func (u *upstreamServer) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	u.ServeHTTP(rec, req)
	rec.Flush()

	resp := newResponse(rec.Code, rec.Body.Bytes())
	resp.Header = rec.HeaderMap
	return resp, nil
}

func cc(cc string) string {
	return fmt.Sprintf("Cache-Control: %s", cc)
}

func readAll(r io.Reader) []byte {
	b, err := ioutil.ReadAll(r)
	if err != nil {
		panic(err)
	}
	return b
}

func readAllString(r io.Reader) string {
	return string(readAll(r))
}
