package match

import (
	"net/http"
	"regexp"
	"strings"
)

// ContentTypeIs returns a RespCondition testing whether the HTTP response has Content-Type header equal
// to one of the given strings.
func ContentTypeIs(typ string, types ...string) RespCondition {
	types = append(types, typ)
	return RespConditionFunc(func(resp *http.Response, ctx *ProxyCtx) bool {
		if resp == nil {
			return false
		}
		contentType := resp.Header.Get("Content-Type")
		for _, typ := range types {
			if contentType == typ || strings.HasPrefix(contentType, typ+";") {
				return true
			}
		}
		return false
	})
}

// UrlHasPrefix returns a ReqCondtion checking wether the destination URL the proxy client has requested
// has the given prefix, with or without the host.
// For example UrlHasPrefix("host/x") will match requests of the form 'GET host/x', and will match
// requests to url 'http://host/x'
func UrlHasPrefix(prefix string) ReqConditionFunc {
	return func(req *http.Request, ctx *ProxyCtx) bool {
		return strings.HasPrefix(req.URL.Path, prefix) ||
			strings.HasPrefix(req.URL.Host+"/"+req.URL.Path, prefix) ||
			strings.HasPrefix(req.URL.Scheme+req.URL.Host+req.URL.Path, prefix)
	}
}

// UrlIs returns a ReqCondition, testing whether or not the request URL is one of the given strings
// with or without the host prefix.
// UrlIs("google.com/","foo") will match requests 'GET /' to 'google.com', requests `'GET google.com/' to
// any host, and requests of the form 'GET foo'.
func UrlIs(urls ...string) ReqConditionFunc {
	urlSet := make(map[string]bool)
	for _, u := range urls {
		urlSet[u] = true
	}
	return func(req *http.Request, ctx *ProxyCtx) bool {
		_, pathOk := urlSet[req.URL.Path]
		_, hostAndOk := urlSet[req.URL.Host+req.URL.Path]
		return pathOk || hostAndOk
	}
}

// ReqHostMatches returns a ReqCondition, testing whether the host to which the request was directed to matches
// any of the given regular expressions.
func ReqHostMatches(regexps ...*regexp.Regexp) ReqConditionFunc {
	return func(req *http.Request, ctx *ProxyCtx) bool {
		for _, re := range regexps {
			if re.MatchString(req.Host) {
				return true
			}
		}
		return false
	}
}

// ReqHostIs returns a ReqCondition, testing whether the host to which the request is directed to equal
// to one of the given strings
func ReqHostIs(hosts ...string) ReqConditionFunc {
	hostSet := make(map[string]bool)
	for _, h := range hosts {
		hostSet[h] = true
	}
	return func(req *http.Request, ctx *ProxyCtx) bool {
		_, ok := hostSet[req.URL.Host]
		return ok
	}
}

var localHostIpv4 = regexp.MustCompile(`127\.0\.0\.\d+`)

// IsLocalHost checks whether the destination host is explicitly local host
// (buggy, there can be IPv6 addresses it doesn't catch)
var IsLocalHost ReqConditionFunc = func(req *http.Request, ctx *ProxyCtx) bool {
	return req.URL.Host == "::1" ||
		req.URL.Host == "0:0:0:0:0:0:0:1" ||
		localHostIpv4.MatchString(req.URL.Host) ||
		req.URL.Host == "localhost"
}

// UrlMatches returns a ReqCondition testing whether the destination URL
// of the request matches the given regexp, with or without prefix
func UrlMatches(re *regexp.Regexp) ReqConditionFunc {
	return func(req *http.Request, ctx *ProxyCtx) bool {
		return re.MatchString(req.URL.Path) ||
			re.MatchString(req.URL.Host+req.URL.Path)
	}
}

// DstHostIs returns a ReqCondtion testing wether the host in the request url is the given string
func DstHostIs(host string) ReqConditionFunc {
	return func(req *http.Request, ctx *ProxyCtx) bool {
		return req.URL.Host == host
	}
}

// SrcIpIs returns a ReqCondtion testing wether the source IP of the request is the given string
func SrcIpIs(ip string) ReqCondition {
	return ReqConditionFunc(func(req *http.Request, ctx *ProxyCtx) bool {
		return strings.HasPrefix(req.RemoteAddr, ip+":")
	})
}

// Not returns a ReqCondtion negating the given ReqCondition
func Not(r ReqCondition) ReqConditionFunc {
	return func(req *http.Request, ctx *ProxyCtx) bool {
		return !r.HandleReq(req, ctx)
	}
}
