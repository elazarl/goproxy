package main

import (
	"github.com/elazarl/goproxy"
	"github.com/elazarl/goproxy/ext/html"
	"log"
	"net/http"
	"regexp"
)

func findScriptSrc(html string) []string {
	// who said we can't parse HTML with regexp?
	scriptMatcher := regexp.MustCompile(`(?i:<script\s+)`)
	srcAttrMatcher := regexp.MustCompile(`^(?i:[^>]*\ssrc=["']([^"']*)["'])`)

	srcs := make([]string, 0)
	matches := scriptMatcher.FindAllStringIndex(html, -1)
	for _, match := range matches {
		//println("Match",html[match[0]:match[1]])
		// -1 to capture the whitespace at the end of the script tag
		srcMatch := srcAttrMatcher.FindStringSubmatch(html[match[1]-1:])
		if srcMatch != nil {
			srcs = append(srcs, srcMatch[1])
		}
	}
	return srcs
}

func NewJqueryVersionProxy() *goproxy.ProxyHttpServer {
	proxy := goproxy.NewProxyHttpServer()
	m := make(map[string]string)
	jqueryMatcher := regexp.MustCompile(`(?i:jquery\.)`)
	proxy.OnResponse(goproxy_html.IsHtml).Do(goproxy_html.HandleString(
		func(s string, ctx *goproxy.ProxyCtx) string {
			//ctx.Warnf("Charset %v by %v",ctx.Charset(),ctx.Req.Header["Content-Type"])
			for _, src := range findScriptSrc(s) {
				if jqueryMatcher.MatchString(src) {
					prev, ok := m[ctx.Req.Host]
					if ok && prev != src {
						ctx.Warnf("In %v, Contradicting jqueries %v %v", ctx.Req.URL, prev, src)
						break
					}
					m[ctx.Req.Host] = src
				}
			}
			return s
		}))
	return proxy
}

func main() {
	proxy := NewJqueryVersionProxy()
	//proxy.Verbose = true
	log.Fatal(http.ListenAndServe(":8080", proxy))
}
