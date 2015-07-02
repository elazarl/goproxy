package main

import (
	"bytes"
	"log"
	"net/http"
	"time"
	"fmt"
	"math"
	"github.com/lox/httpcache"
	"github.com/elazarl/goproxy"
	"sync"
)

const (
	ProxyDateHeader = "Proxy-Date"
)

var Writes sync.WaitGroup

func TryCacheResponse(shared bool, cache httpcache.Cache, r *http.Response) (*http.Response) {
	if (r.StatusCode != http.StatusOK || r.StatusCode != http.StatusNotModified){
		return r
	}
	
	cReq, err := NewCacheRequest(r.Request)
	if err != nil {
		return goproxy.NewResponse(r.Request, goproxy.ContentTypeText, http.StatusInternalServerError, err.Error())
	}
	
	if !cReq.IsCacheable() {
		log.Printf("request not cacheable")
		return r
	}
	
	cacheType := "private"
	if shared {
		cacheType = "shared"
	}
		
	r.Header.Set(ProxyDateHeader, httpcache.Clock().Format(http.TimeFormat))
	
	res := httpcache.NewResource(r.StatusCode, r.Body, r.Header)
	
	if (r.StatusCode == http.StatusOK){
		storeResource(shared, cache, res, cReq)		
	}
	
	if (r.StatusCode == http.StatusNotModified){
		cache.Freshen(res, cReq.Key.ForMethod("GET").String())
	}
	
	return r
}

func TryServeCachedResponse(shared bool, cache httpcache.Cache, r *http.Request) (*http.Request, *http.Response) {
	cReq, err := NewCacheRequest(r)
	if err != nil {
		return r, goproxy.NewResponse(r, goproxy.ContentTypeText, http.StatusInternalServerError, err.Error())
	}
	
	if !cReq.IsCacheable() {
		log.Printf("request not cacheable")
		return r, nil
	}
	
	cacheType := "private"
	if shared {
		cacheType = "shared"
	}
	
	res, err := lookup(cache, cReq)
	if err != nil && err != httpcache.ErrNotFoundInCache {
		return r, goproxy.NewResponse(r, goproxy.ContentTypeText, http.StatusInternalServerError, err.Error())
	}
	
	if err == httpcache.ErrNotFoundInCache {
		log.Printf("%s %s not in %s cache", r.Method, r.URL.String(), cacheType)
		if cReq.CacheControl.Has("only-if-cached") {
			return r, goproxy.NewResponse(r, goproxy.ContentTypeText, http.StatusGatewayTimeout, "key not in cache")
		}
		
		return r, nil
	} 
	
	log.Printf("%s %s found in %s cache", r.Method, r.URL.String(), cacheType)

	if needsValidation(shared, res, cReq) {
		if cReq.CacheControl.Has("only-if-cached") {
			return r, goproxy.NewResponse(r, goproxy.ContentTypeText, http.StatusGatewayTimeout, "key not in cache")
		}

		log.Printf("validating cached response")		
		return r, nil
	}
	
	log.Printf("serving from cache")
	
	response := NewCachedResponse(shared, r, cReq, res)
	
	return r, response
}

func NewCachedResponse(shared bool, r *http.Request, cReq *cacheRequest, res * httpcache.Resource) *http.Response {	
	age, err := res.Age()
	if err != nil {
		return goproxy.NewResponse(r, goproxy.ContentTypeText, http.StatusInternalServerError, "Error calculating age: "+err.Error())
	}	
	
	contentLength, err := res.ReadSeekCloser.Seek(0, 2)
	if err != nil {
		return goproxy.NewResponse(r, goproxy.ContentTypeText, http.StatusInternalServerError, "Error getting resource length: "+err.Error())
	}	
		
	res.ReadSeekCloser.Seek(0, 0)
	
	resp := &http.Response{}
	resp.Request = r
	resp.TransferEncoding = r.TransferEncoding
	resp.Header = make(http.Header)
	
	for key, headers := range res.Header() {
		for _, header := range headers {
			resp.Header.Add(key, header)
		}
	}
	
	// http://httpwg.github.io/specs/rfc7234.html#warn.113
	if age > (time.Hour*24) && res.HeuristicFreshness() > (time.Hour*24) {
		resp.Header.Add("Warning", `113 - "Heuristic Expiration"`)
	}

	// http://httpwg.github.io/specs/rfc7234.html#warn.110
	freshness, err := freshness(shared, res, cReq)
	if err != nil || freshness <= 0 {
		resp.Header.Add("Warning", `110 - "Response is Stale"`)
	}

	log.Printf("resource is %s old, updating age from %s", age.String(), resp.Header.Get("Age"))
		
	resp.Header.Set("Age", fmt.Sprintf("%.f", math.Floor(age.Seconds())))
	resp.Header.Set("Via", res.Via())
	resp.ContentLength = contentLength
	resp.StatusCode = res.Status()	
	
	if resp.StatusCode != http.StatusOK {
		resp.Body = &ClosingBuffer{bytes.NewBufferString("")}
	} else {
		resp.Body =	res.ReadSeekCloser
	}
	
	return resp
}

type ClosingBuffer struct { 
        *bytes.Buffer 
} 

func (cb *ClosingBuffer) Close() (err error) { 
        //we don't actually have to do anything here, since the buffer is just some data in memory 
        //and the error is initialized to no-error 
        return 
} 

func storeResource(shared bool, cache httpcache.Cache, res *httpcache.Resource, r *cacheRequest) {
	Writes.Add(1)

	go func() {
		defer Writes.Done()
		t := httpcache.Clock()
		keys := []string{r.Key.String()}
		headers := res.Header()

		if shared {
			res.RemovePrivateHeaders()
		}

		// store a secondary vary version
		if vary := headers.Get("Vary"); vary != "" {
			keys = append(keys, r.Key.Vary(vary, r.Request).String())
		}

		if err := cache.Store(res, keys...); err != nil {
			log.Printf("storing resources %#v failed with error: %s", keys, err.Error())
		}

		log.Printf("stored resources %+v in %s", keys, httpcache.Clock().Sub(t))
	}()
}

func lookup(cache httpcache.Cache, req *cacheRequest) (*httpcache.Resource, error) {
	cacheKey := req.Key.String()
	res, err := cache.Retrieve(cacheKey)

	// HEAD requests can possibly be served from GET
	if err == httpcache.ErrNotFoundInCache && req.Method == "HEAD" {
		res, err = cache.Retrieve(req.Key.ForMethod("GET").String())
		if err != nil {
			return nil, err
		}

		if res.HasExplicitExpiration() && req.IsCacheable() {
			return res, nil
		} else {
			return nil, httpcache.ErrNotFoundInCache
		}
	} else if err != nil {
		return res, err
	}

	// Secondary lookup for Vary
	if vary := res.Header().Get("Vary"); vary != "" {
		res, err = cache.Retrieve(req.Key.Vary(vary, req.Request).String())
		if err != nil {
			return res, err
		}
	}

	return res, nil
}

func needsValidation(shared bool, res * httpcache.Resource, r *cacheRequest) bool {
	if res.MustValidate(shared) {
		return true
	}

	freshness, err := freshness(shared, res, r)
	if err != nil {
		log.Printf("error calculating freshness: %s", err.Error())
		return true
	}

	if r.CacheControl.Has("min-fresh") {
		reqMinFresh, err := r.CacheControl.Duration("min-fresh")
		if err != nil {
			log.Printf("error parsing request min-fresh: %s", err.Error())
			return true
		}

		if freshness < reqMinFresh {
			log.Printf("resource is fresh, but won't satisfy min-fresh of %s", reqMinFresh)
			return true
		}
	}

	log.Printf("resource has a freshness of %s", freshness)

	if freshness <= 0 && r.CacheControl.Has("max-stale") {
		if len(r.CacheControl["max-stale"]) == 0 {
			log.Printf("resource is stale, but client sent max-stale")
			return false
		} else if maxStale, _ := r.CacheControl.Duration("max-stale"); maxStale >= (freshness * -1) {
			log.Printf("resource is stale, but within allowed max-stale period of %s", maxStale)
			return false
		}
	}

	return freshness <= 0
}

// freshness returns the duration that a requested resource will be fresh for
func freshness(shared bool, res * httpcache.Resource, r *cacheRequest) (time.Duration, error) {
	maxAge, err := res.MaxAge(shared)
	if err != nil {
		return time.Duration(0), err
	}

	if r.CacheControl.Has("max-age") {
		reqMaxAge, err := r.CacheControl.Duration("max-age")
		if err != nil {
			return time.Duration(0), err
		}

		if reqMaxAge < maxAge {
			log.Printf("using request max-age of %s", reqMaxAge.String())
			maxAge = reqMaxAge
		}
	}

	age, err := res.Age()
	if err != nil {
		return time.Duration(0), err
	}

	if res.IsStale() {
		return time.Duration(0), nil
	}

	if hFresh := res.HeuristicFreshness(); hFresh > maxAge {
		log.Printf("using heuristic freshness of %q", hFresh)
		maxAge = hFresh
	}

	return maxAge - age, nil
}