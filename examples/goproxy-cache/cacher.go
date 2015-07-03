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
	"io"
	"net/textproto"
)

const (
	ProxyDateHeader = "Proxy-Date"
)

var Writes sync.WaitGroup

func TryCacheResponse(shared bool, cache httpcache.Cache, response *http.Response) (*http.Response) {
	if (response.StatusCode != http.StatusOK || response.StatusCode != http.StatusNotModified){
		return response
	}
	
	cacheRequest, err := NewCacheRequest(response.Request)
	if err != nil {
		return goproxy.NewResponse(response.Request, goproxy.ContentTypeText, http.StatusInternalServerError, err.Error())
	}
	
	if !cacheRequest.IsCacheable() {
		log.Printf("request not cacheable")
		return response
	}
		
	if (response.StatusCode == http.StatusNotModified){		
		resource, err := lookup(cache, cacheRequest)
		if err != nil && err != httpcache.ErrNotFoundInCache {
			return goproxy.NewResponse(response.Request, goproxy.ContentTypeText, http.StatusInternalServerError, err.Error())
		} else if err == httpcache.ErrNotFoundInCache {
			return response
		}
		
		cache.Freshen(resource, cacheRequest.Key.ForMethod("GET").String())
		response := newCachedResponse(shared, response.Request, cacheRequest, resource)
		
		return response
	}
	
	if (response.StatusCode == http.StatusOK){
		if err != httpcache.ErrNotFoundInCache {
			return response
		}		
		
		cachedResponse := newCacheAndForwardResponse(shared, cache, response.Request, cacheRequest, response)
				
		return cachedResponse
	}
	
	return response
}

func TryServeCachedResponse(shared bool, cache httpcache.Cache, request *http.Request) (*http.Request, *http.Response) {
	cacheRequest, err := NewCacheRequest(request)
	if err != nil {
		return request, goproxy.NewResponse(request, goproxy.ContentTypeText, http.StatusInternalServerError, err.Error())
	}
	
	if !cacheRequest.IsCacheable() {
		log.Printf("request not cacheable")
		return request, nil
	}
	
	cacheType := "private"
	if shared {
		cacheType = "shared"
	}
	
	resource, err := lookup(cache, cacheRequest)
	if err != nil && err != httpcache.ErrNotFoundInCache {
		return request, goproxy.NewResponse(request, goproxy.ContentTypeText, http.StatusInternalServerError, err.Error())
	}
	
	if err == httpcache.ErrNotFoundInCache {
		log.Printf("%s %s not in %s cache", request.Method, request.URL.String(), cacheType)
		if cacheRequest.CacheControl.Has("only-if-cached") {
			return request, goproxy.NewResponse(request, goproxy.ContentTypeText, http.StatusGatewayTimeout, "key not in cache")
		}
		
		return request, nil
	} 
	
	log.Printf("%s %s found in %s cache", request.Method, request.URL.String(), cacheType)

	if needsValidation(shared, resource, cacheRequest) {
		if cacheRequest.CacheControl.Has("only-if-cached") {
			return request, goproxy.NewResponse(request, goproxy.ContentTypeText, http.StatusGatewayTimeout, "key not in cache")
		}

		log.Printf("validating cached response")		
		return request, nil
	}
	
	log.Printf("serving from cache")
	
	response := newCachedResponse(shared, request, cacheRequest, resource)
	
	return request, response
}

func newCacheAndForwardResponse(shared bool, cache httpcache.Cache, request *http.Request, cacheRequest *cacheRequest, response * http.Response) *http.Response {
	return response		
}


func newCachedResponse(shared bool, request *http.Request, cacheRequest *cacheRequest, resource * httpcache.Resource) *http.Response {	
	age, err := resource.Age()
	if err != nil {
		return goproxy.NewResponse(request, goproxy.ContentTypeText, http.StatusInternalServerError, "Error calculating age: "+err.Error())
	}	
	
	contentLength, err := resource.ReadSeekCloser.Seek(0, 2)
	if err != nil {
		return goproxy.NewResponse(request, goproxy.ContentTypeText, http.StatusInternalServerError, "Error getting resource length: "+err.Error())
	}	
	resource.ReadSeekCloser.Seek(0, 0)
	
	statusCode := resource.Status()
	
	headers := make(http.Header)
	
	for key, mimeheaders := range resource.Header() {
		for _, header := range mimeheaders {
			textproto.MIMEHeader(headers).Add(key, header)
		}
	}
	
	// http://httpwg.github.io/specs/rfc7234.html#warn.113
	if age > (time.Hour*24) && resource.HeuristicFreshness() > (time.Hour*24) {
		textproto.MIMEHeader(headers).Add("Warning", `113 - "Heuristic Expiration"`)
	}

	// http://httpwg.github.io/specs/rfc7234.html#warn.110
	freshness, err := freshness(shared, resource, cacheRequest)
	if err != nil || freshness <= 0 {
		textproto.MIMEHeader(headers).Add("Warning", `110 - "Response is Stale"`)
	}

	log.Printf("resource is %s old, updating age from %s", age.String(), headers.Get("Age"))
		
	textproto.MIMEHeader(headers).Set("Age", fmt.Sprintf("%.f", math.Floor(age.Seconds())))
	textproto.MIMEHeader(headers).Set("Via", resource.Via())
	
	body := resource.ReadSeekCloser
	
	response := newResponse(request, headers, body, contentLength, statusCode)
	
	return response
}

func newResponse(request *http.Request, headers http.Header, body io.ReadCloser, contentLength int64, statusCode int) *http.Response {
	response := &http.Response{}
	response.Request = request
	response.TransferEncoding = request.TransferEncoding
	response.Header = headers
	response.ContentLength = contentLength
	response.StatusCode = statusCode	
	
	if request.Method == "HEAD" || response.StatusCode != http.StatusOK {
		response.Body = &BufferReadCloser{bytes.NewBufferString("")}
	} else {
		response.Body =	body
	}
	
	return response
}


func lookup(cache httpcache.Cache, cacheRequest *cacheRequest) (*httpcache.Resource, error) {
	cacheKey := cacheRequest.Key.String()
	resource, err := cache.Retrieve(cacheKey)

	// HEAD requests can possibly be served from GET
	if err == httpcache.ErrNotFoundInCache && cacheRequest.Method == "HEAD" {
		resource, err = cache.Retrieve(cacheRequest.Key.ForMethod("GET").String())
		if err != nil {
			return nil, err
		}

		if resource.HasExplicitExpiration() && cacheRequest.IsCacheable() {
			return resource, nil
		} else {
			return nil, httpcache.ErrNotFoundInCache
		}
	} else if err != nil {
		return resource, err
	}

	// Secondary lookup for Vary
	if vary := resource.Header().Get("Vary"); vary != "" {
		resource, err = cache.Retrieve(cacheRequest.Key.Vary(vary, cacheRequest.Request).String())
		if err != nil {
			return resource, err
		}
	}

	return resource, nil
}

func needsValidation(shared bool, resource * httpcache.Resource, cacheRequest *cacheRequest) bool {
	if resource.MustValidate(shared) {
		return true
	}

	freshness, err := freshness(shared, resource, cacheRequest)
	if err != nil {
		log.Printf("error calculating freshness: %s", err.Error())
		return true
	}

	if cacheRequest.CacheControl.Has("min-fresh") {
		reqMinFresh, err := cacheRequest.CacheControl.Duration("min-fresh")
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

	if freshness <= 0 && cacheRequest.CacheControl.Has("max-stale") {
		if len(cacheRequest.CacheControl["max-stale"]) == 0 {
			log.Printf("resource is stale, but client sent max-stale")
			return false
		} else if maxStale, _ := cacheRequest.CacheControl.Duration("max-stale"); maxStale >= (freshness * -1) {
			log.Printf("resource is stale, but within allowed max-stale period of %s", maxStale)
			return false
		}
	}

	return freshness <= 0
}

// freshness returns the duration that a requested resource will be fresh for
func freshness(shared bool, resource * httpcache.Resource, cacheRequest *cacheRequest) (time.Duration, error) {
	maxAge, err := resource.MaxAge(shared)
	if err != nil {
		return time.Duration(0), err
	}

	if cacheRequest.CacheControl.Has("max-age") {
		reqMaxAge, err := cacheRequest.CacheControl.Duration("max-age")
		if err != nil {
			return time.Duration(0), err
		}

		if reqMaxAge < maxAge {
			log.Printf("using request max-age of %s", reqMaxAge.String())
			maxAge = reqMaxAge
		}
	}

	age, err := resource.Age()
	if err != nil {
		return time.Duration(0), err
	}

	if resource.IsStale() {
		return time.Duration(0), nil
	}

	if hFresh := resource.HeuristicFreshness(); hFresh > maxAge {
		log.Printf("using heuristic freshness of %q", hFresh)
		maxAge = hFresh
	}

	return maxAge - age, nil
}