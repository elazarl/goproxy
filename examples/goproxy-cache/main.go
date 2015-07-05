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

var matchUbuntuAptPattern = regexp.MustCompile(`(security|archive).ubuntu.com/ubuntu/(.+)$`)
var matchAptPackagePattern = regexp.MustCompile(`(deb|udeb)$`)
var matchAptPackageIndexPattern = regexp.MustCompile(`(DiffIndex|PackagesIndex|Packages\.(bz2|gz|lzma)|SourcesIndex|Sources\.(bz2|gz|lzma)|Release(\.gpg)?|Translation-(en|fr)\.(gz|bz2|bzip2|lzma))$`)
var matchWindowsUpdatePackageIndexPattern = regexp.MustCompile(`(microsoft|windowsupdate|windows).com/.*\.(cab|exe|ms[i|u|f]|[ap]sf|wm[v|a]|dat|zip)`)

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

	matchUbuntuApt := goproxy.UrlMatches(matchUbuntuAptPattern)
	matchAptPackage := goproxy.UrlMatches(matchAptPackagePattern)
	matchAptPackageIndex := goproxy.UrlMatches(matchAptPackageIndexPattern)
	matchWindowsUpdatePackageIndex := goproxy.UrlMatches(matchWindowsUpdatePackageIndexPattern)

	proxy.OnRequest(matchUbuntuApt).DoFunc(
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

	proxy.OnRequest(matchWindowsUpdatePackageIndex).DoFunc(
		func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
			return TryServeCachedResponse(true, cache, r)
		})

	proxy.OnResponse(matchWindowsUpdatePackageIndex).DoFunc(
		func(r *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
			return TryCacheResponse(true, cache, r)
		})

	log.Printf("proxy listening on %s", listen)
	log.Fatal(http.ListenAndServe(listen, proxy))
}
