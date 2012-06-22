package goproxy

import (
	"bufio"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
)

type ConnectActionLiteral int

const (
	ConnectAccept = iota
	ConnectReject
	ConnectMitm
)

var (
	OkConnect     = &ConnectAction{Action: ConnectAccept}
	MitmConnect   = &ConnectAction{Action: ConnectMitm}
	RejectConnect = &ConnectAction{Action: ConnectReject}
)

type ConnectAction struct {
	Action    ConnectActionLiteral
	TlsConfig *tls.Config
}

func (proxy *ProxyHttpServer) handleHttps(w http.ResponseWriter, r *http.Request) {
	ctx := &ProxyCtx{Req: r, sess: atomic.AddInt32(&proxy.sess, 1), proxy: proxy}

	hij, ok := w.(http.Hijacker)
	if !ok {
		panic("httpserver does not support hijacking")
	}

	host := r.URL.Host
	if !hasPort.MatchString(host) {
		host += ":80"
	}
	targetSiteCon, e := net.Dial("tcp", host)
	if e != nil {
		// trying to mimic the behaviour of the offending website
		// don't answer at all
		return
	}
	proxyClient, _, e := hij.Hijack()
	if e != nil {
		panic("Cannot hijack connection " + e.Error())
	}

	ctx.Logf("Running %d CONNECT handlers", len(proxy.httpsHandlers))
	todo := OkConnect
	ctx.Req = r
	for _, h := range proxy.httpsHandlers {
		todo = h.HandleConnect(r.Host, ctx)
		ctx.Logf("handler: %v", todo)
	}
	switch todo.Action {
	case ConnectAccept:
		ctx.Logf("Accepting CONNECT")
		proxyClient.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))
		go proxy.copyAndClose(targetSiteCon, proxyClient)
		go proxy.copyAndClose(proxyClient, targetSiteCon)
	case ConnectMitm:
		proxyClient.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))
		ctx.Logf("Assuming CONNECT is TLS, mitm proxying it")
		// this goes in a separate goroutine, so that the net/http server won't think we're
		// still handling the request even after hijacking the connection. Those HTTP CONNECT
		// request can take forever, and the server will be stuck when "closed".
		// TODO: Allow Server.Close() mechanism to shut down this connection as nicely as possible
		tlsConfig := todo.TlsConfig
		if tlsConfig == nil {
			tlsConfig = defaultTlsConfig
		}
		go func() {

			//TODO: cache connections to the remote website
			rawClientTls := tls.Server(proxyClient, tlsConfig)
			if err := rawClientTls.Handshake(); err != nil {
				ctx.Warnf("Cannot handshake client %v %v", r.Host, err)
			}
			defer rawClientTls.Close()
			clientTlsReader := bufio.NewReader(rawClientTls)
			for !isEof(clientTlsReader) {
				req, err := http.ReadRequest(clientTlsReader)
				if err != nil {
					ctx.Warnf("Cannot read TLS request from mitm'd client %v %v", r.Host, err)
					return
				}
				ctx.Logf("req %v", r.Host)
				req, resp := proxy.filterRequest(req, ctx)
				if resp == nil {
					req.URL, err = url.Parse("https://" + r.Host + req.URL.Path)
					if err != nil {
						ctx.Warnf("Illegal URL %s", "https://"+r.Host+req.URL.Path)
						return
					}
					resp, err = proxy.tr.RoundTrip(req)
					if err != nil {
						ctx.Warnf("Cannot read TLS response from mitm'd server %v", err)
						return
					}
					ctx.Logf("resp %v", resp.Status)
				}
				resp = proxy.filterResponse(resp, ctx)
				if err := resp.Write(rawClientTls); err != nil {
					ctx.Warnf("Cannot write TLS response from mitm'd client %v", err)
					return
				}
			}
			ctx.Logf("Exiting on EOF")
		}()
	case ConnectReject:
		targetSiteCon.Close()
		proxyClient.Close()
	}
}
