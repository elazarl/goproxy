package goproxy

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"

	"golang.org/x/net/http/httpproxy"
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
	Ca        *tls.Certificate
}

func stripPort(s string) string {
	ix := strings.IndexRune(s, ':')
	if ix == -1 {
		return s
	}
	return s[:ix]
}

func httpsProxyFromEnv(reqURL *url.URL) (string, error) {
	cfg := httpproxy.FromEnvironment()
	// We only use this codepath for HTTPS CONNECT proxies so we shouldn't
	// return anything from HTTPProxy
	cfg.HTTPProxy = ""

	// The request URL provided to the proxy for a CONNECT request does
	// not necessarily have an https scheme but ProxyFunc uses the scheme
	// to determine which env var to introspect.
	reqSchemeURL := reqURL
	reqSchemeURL.Scheme = "https"

	proxyURL, err := cfg.ProxyFunc()(reqSchemeURL)
	if err != nil {
		return "", err
	}
	if proxyURL == nil {
		return "", nil
	}

	service := proxyURL.Port()
	if service == "" {
		service = proxyURL.Scheme
	}

	return fmt.Sprintf("%s:%s", proxyURL.Hostname(), service), nil
}

func (proxy *ProxyHttpServer) dial(network, addr string, userdata interface{}) (c net.Conn, err error) {
	if proxy.Tr.Dial != nil {
		return proxy.Tr.Dial(network, addr, userdata)
	}
	return net.Dial(network, addr)
}

func (proxy *ProxyHttpServer) handleHttps(w http.ResponseWriter, r *http.Request) {
	ctx := &ProxyCtx{Req: r, Session: atomic.AddInt64(&proxy.sess, 1), proxy: proxy}

	hij, ok := w.(http.Hijacker)
	if !ok {
		panic("httpserver does not support hijacking")
	}

	proxyClient, _, e := hij.Hijack()
	if e != nil {
		panic("Cannot hijack connection " + e.Error())
	}

	ctx.Logf("Running %d CONNECT handlers", len(proxy.httpsHandlers))
	todo, host := OkConnect, r.URL.Host
	ctx.Req = r
	for _, h := range proxy.httpsHandlers {
		newtodo, newhost := h.HandleConnect(host, ctx)
		if newtodo != nil {
			todo, host = newtodo, newhost
		}
		ctx.Logf("handler: %v %s", todo, host)
	}
	switch todo.Action {
	case ConnectAccept:
		if !hasPort.MatchString(host) {
			host += ":80"
		}
		https_proxy, err := httpsProxyFromEnv(r.URL)
		if err != nil {
			ctx.Warnf("Error configuring HTTPS proxy: %s", err)
		}
		var targetSiteCon net.Conn
		var e error
		if https_proxy != "" {
			targetSiteCon, e = proxy.dial("tcp", https_proxy, ctx.UserData)
		} else {
			targetSiteCon, e = proxy.dial("tcp", host, ctx.UserData)
		}
		if e != nil {
			if _, err := io.WriteString(proxyClient, "HTTP/1.1 502 Bad Gateway\r\n\r\n"); err != nil {
				ctx.Warnf("Error responding to client: %s", err)
			}
			ctx.Warnf("Error dialing to %s: %s", host, e.Error())
			if err := proxyClient.Close(); err != nil {
				ctx.Warnf("Error closing client connection: %s", err)
			}
			return
		}
		if https_proxy != "" {
			connectReq := &http.Request{
				Method: "CONNECT",
				URL:    &url.URL{Opaque: host},
				Host:   host,
				Header: make(http.Header),
			}
			connectReq.Write(targetSiteCon)

			// Read response.
			// Okay to use and discard buffered reader here, because
			// TLS server will not speak until spoken to.
			br := bufio.NewReader(targetSiteCon)
			resp, err := http.ReadResponse(br, connectReq)
			if err != nil {
				targetSiteCon.Close()
				w.WriteHeader(500)
				return
			}
			if resp.StatusCode != 200 {
				targetSiteCon.Close()
				w.WriteHeader(resp.StatusCode)
				io.Copy(w, resp.Body)
				resp.Body.Close()
				return
			}
		}
		ctx.Logf("Accepting CONNECT to %s", host)
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
		ca := todo.Ca
		if ca == nil {
			ca = &GoproxyCa
		}
		cert, err := signHost(*ca, []string{stripPort(host)})
		if err != nil {
			ctx.Warnf("Cannot sign host certificate with provided CA: %s", err)
			return
		}
		tlsConfig := tls.Config{}
		if todo.TlsConfig != nil {
			tlsConfig = *todo.TlsConfig
		} else {
			tlsConfig = *defaultTlsConfig
		}
		tlsConfig.Certificates = append(tlsConfig.Certificates, cert)
		go func() {
			//TODO: cache connections to the remote website
			rawClientTls := tls.Server(proxyClient, &tlsConfig)
			if err := rawClientTls.Handshake(); err != nil {
				ctx.Warnf("Cannot handshake client %v %v", r.Host, err)
				return
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
					req.URL, err = url.Parse("https://" + r.Host + req.URL.String())
					if err != nil {
						ctx.Warnf("Illegal URL %s", "https://"+r.Host+req.URL.Path)
						return
					}
					removeProxyHeaders(ctx, req)
					resp, err = proxy.Tr.RoundTrip(req, ctx.UserData)
					if err != nil {
						ctx.Warnf("Cannot read TLS response from mitm'd server %v", err)
						return
					}
					ctx.Logf("resp %v", resp.Status)
				}
				resp = proxy.filterResponse(resp, ctx)
				text := resp.Status
				statusCode := strconv.Itoa(resp.StatusCode) + " "
				if strings.HasPrefix(text, statusCode) {
					text = text[len(statusCode):]
				}
				// always use 1.1 to support encoding
				if _, err := io.WriteString(rawClientTls, "HTTP/1.1"+" "+statusCode+text+"\r\n"); err != nil {
					ctx.Warnf("Cannot write TLS response HTTP status from mitm'd client: %v", err)
					return
				}
				// Since we don't know the length of resp, return chunked encoded response
				// TODO: use a more reasonable scheme
				resp.Header.Del("Content-Length")
				resp.Header.Set("Transfer-Encoding", "chunked")
				if err := resp.Header.Write(rawClientTls); err != nil {
					ctx.Warnf("Cannot write TLS response header from mitm'd client: %v", err)
					return
				}
				if _, err = io.WriteString(rawClientTls, "\r\n"); err != nil {
					ctx.Warnf("Cannot write TLS response header end from mitm'd client: %v", err)
					return
				}
				chunked := newChunkedWriter(rawClientTls)
				if _, err := io.Copy(chunked, resp.Body); err != nil {
					ctx.Warnf("Cannot write TLS response body from mitm'd client: %v", err)
					return
				}
				if err := chunked.Close(); err != nil {
					ctx.Warnf("Cannot write TLS chunked EOF from mitm'd client: %v", err)
					return
				}
				if _, err = io.WriteString(rawClientTls, "\r\n"); err != nil {
					ctx.Warnf("Cannot write TLS response chunked trailer from mitm'd client: %v", err)
					return
				}
			}
			ctx.Logf("Exiting on EOF")
		}()
	case ConnectReject:
		if ctx.Resp != nil {
			if err := ctx.Resp.Write(proxyClient); err != nil {
				ctx.Warnf("Cannot write response that reject http CONNECT: %v", err)
			}
		}
		proxyClient.Close()
	}
}
