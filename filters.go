package goproxy

import (
	"net/http"
	"regexp"
	"strings"
)

// RespContentTypeIs is a middleware to filter apply a handler only to those requests matching a given content-type.
//
//  imageHandler := HandlerFunc(func(ctx *ProxyCtx) Next {
//      ... convert ctx.Resp.Body, reinject a new Body with a png, and change the Content-Type
//      return FORWARD  // side-steps further modifications
//      // or return NEXT  // to continue on with other Handlers
//  }
//
//  proxy.HandleRequest(RespContentTypeIs("image/jpeg", "image/gif")(imageHandler))
//
func RespContentTypeIs(types ...string) ChainedHandler {
	return func(chainedHandler Handler) Handler {
		return HandlerFunc(func(ctx *ProxyCtx) Next {
			if ctx.Resp == nil {
				return NEXT
			}

			contentType := ctx.Resp.Header.Get("Content-Type")
			for _, typ := range types {
				if contentType == typ || strings.HasPrefix(contentType, typ+";") {
					return chainedHandler.Handle(ctx)
				}
			}
			return NEXT
		})
	}
}

// // Pre-configured
// var RespContentIsImage = RespContentTypeIs(
// 	"image/gif",
// 	"image/jpeg",
// 	"image/pjpeg",
// 	"application/octet-stream",
// 	"image/png",
// )

// UrlHasPrefix is a middleware matching when the destination URL the proxy client has
// requested has the given prefix, with or without the host.
//
// For example UrlHasPrefix("host/x") will match requests of the form
// 'GET host/x', and will match requests to url 'http://host/x'
func UrlHasPrefix(prefix string) ChainedHandler {
	return func(chainedHandler Handler) Handler {
		return HandlerFunc(func(ctx *ProxyCtx) Next {
			req := ctx.Req
			if strings.HasPrefix(req.URL.Path, prefix) ||
				strings.HasPrefix(req.URL.Host+"/"+req.URL.Path, prefix) ||
				strings.HasPrefix(req.URL.Scheme+req.URL.Host+req.URL.Path, prefix) {
				return chainedHandler.Handle(ctx)
			}
			return NEXT
		})
	}
}

// UrlIsIn is a middleware that tests if the URL matches `urls`, testing
// whether or not the request URL is one of the given strings with or
// without the host prefix.
//
// UrlIsIn("google.com/","foo") will match these three cases:
// * 'GET /' with 'Host: google.com'
// * 'GET google.com/'
// * 'GET /foo' for any host
func UrlIsIn(urls ...string) ChainedHandler {
	urlSet := make(map[string]bool)
	for _, u := range urls {
		urlSet[u] = true
	}

	return func(chainedHandler Handler) Handler {
		return HandlerFunc(func(ctx *ProxyCtx) Next {
			req := ctx.Req
			_, pathOk := urlSet[req.URL.Path]
			_, hostAndPathOk := urlSet[req.URL.Host+req.URL.Path]
			if pathOk || hostAndPathOk {
				return chainedHandler.Handle(ctx)
			} else {
				return NEXT
			}
		})
	}
}

// ReqHostMatches is a middleware that tests whether the host to which
// the request was directed to matches any of the given regular
// expressions.
func ReqHostMatches(regexps ...*regexp.Regexp) ChainedHandler {
	return func(chainedHandler Handler) Handler {
		return HandlerFunc(func(ctx *ProxyCtx) Next {
			for _, re := range regexps {
				if re.MatchString(ctx.Req.Host) {
					return chainedHandler.Handle(ctx)
				}
			}
			return NEXT
		})
	}
}



// RequestHostIsIn is a middleware that tests whether the host to which
// the request is directed to equal to one of the given strings.
//
// This matcher supersedes and combines DstHostIs and ReqHostIs.
func RequestHostIsIn(hosts ...string) ChainedHandler {
	hostSet := HostsToMap(hosts...)

	return func(chainedHandler Handler) Handler {
		return HandlerFunc(func(ctx *ProxyCtx) Next {
			if MatchRequestHostMap(ctx.Req, hostSet) {
				return chainedHandler.Handle(ctx)
			}
			return NEXT
		})
	}
}

func RequestHostIsNotIn(hosts ...string) ChainedHandler {
	hostSet := HostsToMap(hosts...)

	return func(chainedHandler Handler) Handler {
		return HandlerFunc(func(ctx *ProxyCtx) Next {
			if !MatchRequestHostMap(ctx.Req, hostSet) {
				return chainedHandler.Handle(ctx)
			}
			return NEXT
		})
	}
}

func HostsToMap(hosts ...string) map[string]bool {
	hostSet := make(map[string]bool)
	for _, h := range hosts {
		hostSet[h] = true
	}
	return hostSet
}

func MatchRequestHostMap(req *http.Request, hosts map[string]bool) bool {
	 _, ok := hosts[req.URL.Host]
	return ok
}

var localHostIpv4 = regexp.MustCompile(`127\.0\.0\.\d+`)

// IsLocalhost checks whether the destination host is explicitly local host
// (buggy, there can be IPv6 addresses it doesn't catch)
func IsLocalhost(chainedHandler Handler) Handler {
	return HandlerFunc(func(ctx *ProxyCtx) Next {
		if MatchIsLocalhost(ctx.Req) {
			return chainedHandler.Handle(ctx)
		}
		return NEXT
	})
}

func IsNotLocalhost(chainedHandler Handler) Handler {
	return HandlerFunc(func(ctx *ProxyCtx) Next {
		if MatchIsLocalhost(ctx.Req) {
			return chainedHandler.Handle(ctx)
		}
		return NEXT
	})
}

func MatchIsLocalhost(req *http.Request) bool {
	return req.URL.Host == "::1" ||
		req.URL.Host == "0:0:0:0:0:0:0:1" ||
		localHostIpv4.MatchString(req.URL.Host) ||
		req.URL.Host == "localhost"
}

// UrlMatches returns a ReqCondition testing whether the destination URL
// of the request matches the given regexp, with or without prefix
func UrlMatches(re *regexp.Regexp) ChainedHandler {
	return func(chainedHandler Handler) Handler {
		return HandlerFunc(func(ctx *ProxyCtx) Next {
			req := ctx.Req
			if re.MatchString(req.URL.Path) ||
				re.MatchString(req.URL.Host+req.URL.Path) {
				return chainedHandler.Handle(ctx)
			}
			return NEXT
		})
	}
}

// MatchRemoteAddr returns a ReqCondtion testing wether the source IP of the request is the given string, Was renamed from `SrcIpIs`.
func RemoteAddrIs(ip string) ChainedHandler {
	return func(chainedHandler Handler) Handler {
		return HandlerFunc(func(ctx *ProxyCtx) Next {
			if CondRemoteAddrIs(ctx, ip) {
				return chainedHandler.Handle(ctx)
			}
			return NEXT
		})
	}
}

func RemoteAddrIsNot(ip string) ChainedHandler {
	return func(chainedHandler Handler) Handler {
		return HandlerFunc(func(ctx *ProxyCtx) Next {
			if !CondRemoteAddrIs(ctx, ip) {
				return chainedHandler.Handle(ctx)
			}
			return NEXT
		})
	}
}

func CondRemoteAddrIs(ctx *ProxyCtx, ip string) bool {
	return strings.HasPrefix(ctx.Req.RemoteAddr, ip+":")
}

// TODO: implement the other "Not" conditions.
