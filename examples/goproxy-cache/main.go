package main

import (
	"flag"
	"github.com/elazarl/goproxy"
	"github.com/elazarl/goproxy/examples/goproxy-cache/ubuntu"
	"log"
	"net/http"
	"regexp"
)

const (
	defaultListen = "0.0.0.0:3142"
	defaultDir    = "./.aptcache"
)

var (
	version string
	listen  string
	dir     string
	debug   bool
)

var matchUbuntuMirrorPattern = regexp.MustCompile(`(security|archive).ubuntu.com/ubuntu/(.+)$`)

var matchAptPackagePattern = regexp.MustCompile(`(deb|udeb)$`)
var matchAptPackageIndexPattern = regexp.MustCompile(`(DiffIndex|PackagesIndex|Packages\.(bz2|gz|lzma)|SourcesIndex|Sources\.(bz2|gz|lzma)|Release(\.gpg)?|Translation-(en|fr)\.(gz|bz2|bzip2|lzma))$`)

var matchRubyGemPackageIndexPattern = regexp.MustCompile(`^rubygems.org/api/v1/dependencies|^rubygems.org/(prerelease_|latest_)?specs.*\.gz$`)
var matchRubyGemPackagePattern = regexp.MustCompile(`rubygems.org/quick.*gemspec\.rz$|^rubygems.org/gems/.*\.gem$`)

var matchNodePackagePattern = regexp.MustCompile(`^registry.npmjs.org/.*(\.tgz)`)
var matchNodePackageIndexPattern = regexp.MustCompile(`registry.npmjs.org/.*$`)

var matchWindowsUpdatePackagePattern = regexp.MustCompile(`(microsoft|windowsupdate|windows).com/.*\.(cab|exe|ms[i|u|f]|[ap]sf|wm[v|a]|dat|zip)`)

var commonPackagePattern = regexp.MustCompile(`/.*\.(zip|exe|msi|7z|rar|tgz|iso|cab|jar|gz|bz2|bzip2|lzma)`)

func init() {
	flag.StringVar(&listen, "listen", defaultListen, "the host and port to bind to")
	flag.StringVar(&dir, "cachedir", defaultDir, "the dir to store cache data in")
	flag.BoolVar(&debug, "debug", false, "whether to output debugging logging")
	flag.Parse()
}

func main() {
	log.Printf("running apt-proxy %s", version)

	if debug {
		DebugLogging = true
	}

	DebugLogging = true

	cache, err := NewDiskCache(dir)

	if err != nil {
		log.Fatal(err)
	}

	ubuntuRewriter := ubuntu.NewRewriter()

	proxy := goproxy.NewProxyHttpServer()

	matchUbuntuMirror := goproxy.UrlMatches(matchUbuntuMirrorPattern)
	matchAptPackage := goproxy.UrlMatches(matchAptPackagePattern)
	matchAptPackageIndex := goproxy.UrlMatches(matchAptPackageIndexPattern)
	matchRubyGemPackageIndex := goproxy.UrlMatches(matchRubyGemPackageIndexPattern)
	matchRubyGemPackage := goproxy.UrlMatches(matchRubyGemPackagePattern)
	matchNodePackage := goproxy.UrlMatches(matchNodePackagePattern)
	matchNodePackageIndex := goproxy.UrlMatches(matchNodePackageIndexPattern)
	matchWindowsUpdatePackage := goproxy.UrlMatches(matchWindowsUpdatePackagePattern)
	commonPackage := goproxy.UrlMatches(commonPackagePattern)

	proxy.OnRequest(matchUbuntuMirror).DoFunc(
		func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
			url := r.URL.String()
			ubuntuRewriter.Rewrite(r)
			debugf("rewrote %q to %q", url, r.URL.String())
			r.Host = r.URL.Host
			return r, nil
		})

	proxy.OnRequest(matchAptPackage).DoFunc(
		func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
			return TryServeCachedResponse(true, cache, r)
		})

	proxy.OnResponse(matchAptPackage).DoFunc(
		func(r *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
			return TryCacheResponse(true, cache, r)
		})

	proxy.OnRequest(matchAptPackageIndex).DoFunc(
		func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
			return TryServeCachedResponse(true, cache, r)
		})

	proxy.OnResponse(matchAptPackageIndex).DoFunc(
		func(r *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
			return TryCacheResponse(true, cache, r)
		})

	proxy.OnRequest(matchRubyGemPackage).DoFunc(
		func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
			return TryServeCachedResponse(true, cache, r)
		})

	proxy.OnResponse(matchRubyGemPackage).DoFunc(
		func(r *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
			return TryCacheResponse(true, cache, r)
		})

	proxy.OnRequest(matchRubyGemPackageIndex).DoFunc(
		func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
			return TryServeCachedResponse(true, cache, r)
		})

	proxy.OnResponse(matchRubyGemPackageIndex).DoFunc(
		func(r *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
			return TryCacheResponse(true, cache, r)
		})

	proxy.OnRequest(matchNodePackage).DoFunc(
		func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
			return TryServeCachedResponse(true, cache, r)
		})

	proxy.OnResponse(matchNodePackage).DoFunc(
		func(r *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
			return TryCacheResponse(true, cache, r)
		})

	proxy.OnRequest(matchNodePackageIndex).DoFunc(
		func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
			return TryServeCachedResponse(true, cache, r)
		})

	proxy.OnResponse(matchNodePackageIndex).DoFunc(
		func(r *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
			return TryCacheResponse(true, cache, r)
		})

	proxy.OnRequest(matchWindowsUpdatePackage).DoFunc(
		func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
			return TryServeCachedResponse(true, cache, r)
		})

	proxy.OnResponse(matchWindowsUpdatePackage).DoFunc(
		func(r *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
			return TryCacheResponse(true, cache, r)
		})

	proxy.OnRequest(commonPackage).DoFunc(
		func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
			return TryServeCachedResponse(true, cache, r)
		})

	proxy.OnResponse(commonPackage).DoFunc(
		func(r *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
			return TryCacheResponse(true, cache, r)
		})

	log.Printf("proxy listening on %s", listen)
	log.Fatal(http.ListenAndServe(listen, proxy))
}
