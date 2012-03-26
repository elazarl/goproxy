package goproxy

import ("net/http"
	"regexp")

type ProxyCtx struct {
	Req   *http.Request
	Resp  *http.Response
	sess  int32
	proxy *ProxyHttpServer
}

func (ctx *ProxyCtx) Printf(msg string,argv ...interface{}) {
	ctx.proxy.logger.Printf("[%03d] "+msg+"\n",append([]interface{}{ctx.sess & 0xFF},argv...)...)
}
func (ctx *ProxyCtx) Logf(msg string,argv ...interface{}) {
	if ctx.proxy.Verbose {
		ctx.Printf("INFO: "+msg,argv...)
	}
}

func (ctx *ProxyCtx) Warnf(msg string,argv ...interface{}) {
	ctx.Printf("WARN: "+msg,argv...)
}

var charsetFinder = regexp.MustCompile("charset=([^ ]*)")

func (ctx *ProxyCtx) Charset() string {
	charsets := charsetFinder.FindStringSubmatch(ctx.Req.Header.Get("Content-Type"))
	if charsets == nil {
		return ""
	}
	return charsets[0]
}


