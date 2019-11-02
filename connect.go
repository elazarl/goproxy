package goproxy

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
)

type ConnectActionLiteral int

const (
	ConnectAccept = iota
	ConnectReject
	ConnectMitm
	ConnectHijack
	ConnectHTTPMitm
	ConnectProxyAuthHijack
)

var (
	OkConnect       = &ConnectAction{Action: ConnectAccept, TLSConfig: TLSConfigFromCA(&GoproxyCa)}
	MitmConnect     = &ConnectAction{Action: ConnectMitm, TLSConfig: TLSConfigFromCA(&GoproxyCa)}
	HTTPMitmConnect = &ConnectAction{Action: ConnectHTTPMitm, TLSConfig: TLSConfigFromCA(&GoproxyCa)}
	RejectConnect   = &ConnectAction{Action: ConnectReject, TLSConfig: TLSConfigFromCA(&GoproxyCa)}
	httpsRegexp     = regexp.MustCompile(`^https:\/\/`)
)

type ConnectAction struct {
	Action    ConnectActionLiteral
	Hijack    func(req *http.Request, client net.Conn, ctx *ProxyCtx)
	TLSConfig func(host string, ctx *ProxyCtx) (*tls.Config, error)
}

func stripPort(s string) string {
	ix := strings.IndexRune(s, ':')
	if ix == -1 {
		return s
	}
	return s[:ix]
}

func (proxy *ProxyHttpServer) dial(network, addr string) (c net.Conn, err error) {
	if proxy.Tr.Dial != nil {
		return proxy.Tr.Dial(network, addr)
	}
	return net.Dial(network, addr)
}

func (proxy *ProxyHttpServer) connectDial(network, addr string) (c net.Conn, err error) {
	if proxy.ConnectDial == nil {
		return proxy.dial(network, addr)
	}
	return proxy.ConnectDial(network, addr)
}

func (proxy *ProxyHttpServer) handleConnect(w http.ResponseWriter, r *http.Request) {
	hij, ok := w.(http.Hijacker)
	if !ok {
		panic("httpserver does not support hijacking")
	}

	proxyClient, _, e := hij.Hijack()
	if e != nil {
		panic("Cannot hijack connection " + e.Error())
	}

	ctx := &ProxyCtx{
		Req:     r,
		Session: atomic.AddInt64(&proxy.sess, 1),
		proxy:   proxy,
		signer:  signHost,
	}

	if proxy.Signer != nil {
		ctx.signer = proxy.Signer
	}

	// Allow connect per default, using the remote host from the request.
	// Potential handlers for https might change this decision further
	// down.
	todo, host := OkConnect, r.URL.Host

	ctx.Logf("Running %d CONNECT handlers", len(proxy.httpsHandlers))
	for i, h := range proxy.httpsHandlers {
		newtodo, newhost := h.HandleConnect(host, ctx)

		// If found a result, break the loop immediately
		if newtodo != nil {
			todo, host = newtodo, newhost
			ctx.Logf("on %dth handler: %v %s", i, todo, host)
			break
		}
	}

	switch todo.Action {
	case ConnectAccept:
		if !hasPort.MatchString(host) {
			host += ":80"
		}

		targetSite, err := proxy.connectDial("tcp", host)
		if err != nil {
			httpError(proxyClient, ctx, err)
			return
		}

		ctx.Logf("Accepting CONNECT to %s", host)
		proxyClient.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))

		go func() {
			var wg sync.WaitGroup
			wg.Add(2)
			go copyOrWarn(ctx, targetSite, proxyClient, &wg)
			go copyOrWarn(ctx, proxyClient, targetSite, &wg)
			wg.Wait()

			for _, con := range []net.Conn{targetSite, proxyClient} {
				if tcp, ok := con.(*net.TCPConn); ok {
					tcp.CloseWrite()
					tcp.CloseRead()
				}
			}

			proxyClient.Close()
			targetSite.Close()
		}()

	case ConnectHijack:
		ctx.Logf("Hijacking CONNECT to %s", host)
		proxyClient.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))
		todo.Hijack(r, proxyClient, ctx)

	case ConnectHTTPMitm:
		proxyClient.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))
		ctx.Logf("Assuming CONNECT is plain HTTP tunneling, mitm proxying it")

		client := bufio.NewReader(proxyClient)

		var (
			req *http.Request
			err error
		)

		for {
			req, err = http.ReadRequest(client)
			if err != nil {
				if err != io.EOF {
					ctx.Warnf("Cannot read request of MITM HTTP client: %+#v", err)
				}
				break
			}

			req.URL, err = url.Parse("http://" + req.Host + req.URL.String())

			req.RemoteAddr = r.RemoteAddr

			// We create a new context but populate it with the
			// previous UserData in order to be able to correlate
			// the original request to CONNECT with any subsequent
			// request.
			nctx := &ProxyCtx{
				Req:      req,
				Session:  atomic.AddInt64(&proxy.sess, 1),
				proxy:    proxy,
				UserData: ctx.UserData,
			}

			if end, err := proxy.handleRequest(NewConnResponseWriter(proxyClient), req, nctx); end {
				if err != nil {
					ctx.Warnf("Error during serving MITM HTTP request: %+#v", err)
				}
				break
			}

			if req.Close {
				break
			}
		}

		proxyClient.Close()

	case ConnectMitm:
		ctx.Logf("Assuming CONNECT is TLS, mitm proxying it")

		tlsConfig := defaultTLSConfig
		if todo.TLSConfig != nil {
			var err error
			tlsConfig, err = todo.TLSConfig(host, ctx)
			if err != nil {
				httpError(proxyClient, ctx, err)
				return
			}
		}

		proxyClient.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))

		rawClientTls := tls.Server(proxyClient, tlsConfig)
		if err := rawClientTls.Handshake(); err != nil {
			ctx.Warnf("Cannot handshake client %v %v", proxyClient.RemoteAddr(), err)
			return
		}

		clientTls := bufio.NewReader(rawClientTls)

		// [oec, 2019-11-02] debugging helper
		//
		// The following has helped me to track a nasty problem, it
		// might help you too. Use them instead of the clientTls in the
		// line above along with the routines commented out in the
		// for-loop.
		/*
			var previous, current string
			buf := &bytes.Buffer{}
			clientTls := bufio.NewReader(io.TeeReader(rawClientTls, buf))
		*/

		for {
			// Read a request from the client.
			req, err := http.ReadRequest(clientTls)

			// [oec, 2019-11-02] debugging helper
			/*
				previous = current
				current = buf.String()
				buf.Reset()
			*/

			if err != nil {
				if err != io.EOF {
					ctx.Warnf("Cannot read TLS request from mitm'd client for %v: %v", r.Host, err)
					// [oec, 2019-11-02] debugging helper
					/*
						ctx.Warnf("\n[31mprevious request:\n%s«\n[33mcurrent request:\n%s«[0m\n",
							strings.ReplaceAll(previous, "\r\n", "\\r\\n\r\n"),
							strings.ReplaceAll(current, "\r\n", "\\r\\n\r\n"))
					*/
				}
				break
			}

			// We need to read the whole body and reset the buffer.
			// Otherwise, traces of previous requests can be left
			// in the buffer, if the previous request's
			// Content-Length was incorrect or the Body wasn't
			// read.
			body := &bytes.Buffer{}
			io.Copy(body, req.Body)
			req.Body.Close()
			req.Body = ioutil.NopCloser(body)
			clientTls.Reset(rawClientTls)

			if !httpsRegexp.MatchString(req.URL.String()) {
				req.URL, err = url.Parse("https://" + r.Host + req.URL.String())
				if err != nil {
					ctx.Warnf("Couldn't create https-URL for host %q and URL %q: %+#v", r.Host, req.URL.String(), err)
					break
				}
			}

			// We create a new context but populate it with the
			// previous UserData in order to be able to correlate
			// the original request to CONNECT with any subsequent
			// request.
			nctx := &ProxyCtx{
				Req:      req,
				Session:  atomic.AddInt64(&proxy.sess, 1),
				proxy:    proxy,
				UserData: ctx.UserData,
			}

			req.RemoteAddr = r.RemoteAddr
			if end, err := proxy.handleRequest(NewConnResponseWriter(rawClientTls), req, nctx); end {
				if err != nil {
					ctx.Warnf("Error during serving MITM HTTPS request: %v", err)
				}
				break
			}

			if req.Close {
				break
			}
		}

		rawClientTls.Close()

	case ConnectProxyAuthHijack:
		proxyClient.Write([]byte("HTTP/1.1 407 Proxy Authentication Required\r\n"))
		todo.Hijack(r, proxyClient, ctx)

	case ConnectReject:
		if ctx.Resp != nil {
			if err := ctx.Resp.Write(proxyClient); err != nil {
				ctx.Warnf("Cannot write response that reject http CONNECT: %v", err)
			}
		}
		proxyClient.Close()
	}
}

func httpError(w io.WriteCloser, ctx *ProxyCtx, err error) {
	if _, err := io.WriteString(w, "HTTP/1.1 502 Bad Gateway\r\n\r\n"); err != nil {
		ctx.Warnf("Error responding to client: %s", err)
	}
	if err := w.Close(); err != nil {
		ctx.Warnf("Error closing client connection: %s", err)
	}
}

func copyOrWarn(ctx *ProxyCtx, dst io.WriteCloser, src io.ReadCloser, wg *sync.WaitGroup) {
	if _, err := io.Copy(dst, src); err != nil {
		ctx.Warnf("Error copying to client: %s", err)
	}

	// Close both ends, so that another goroutine of this function in the
	// other direction terminates as well.
	src.Close()
	dst.Close()

	wg.Done()
}

func dialerFromEnv(proxy *ProxyHttpServer) func(network, addr string) (net.Conn, error) {
	https_proxy := os.Getenv("HTTPS_PROXY")
	if https_proxy == "" {
		https_proxy = os.Getenv("https_proxy")
	}
	if https_proxy == "" {
		return nil
	}
	return proxy.NewConnectDialToProxy(https_proxy)
}

func (proxy *ProxyHttpServer) NewConnectDialToProxy(https_proxy string) func(network, addr string) (net.Conn, error) {
	return proxy.NewConnectDialToProxyWithHandler(https_proxy, nil)
}

func (proxy *ProxyHttpServer) NewConnectDialToProxyWithHandler(https_proxy string, connectReqHandler func(req *http.Request)) func(network, addr string) (net.Conn, error) {
	u, err := url.Parse(https_proxy)
	if err != nil {
		return nil
	}
	if u.Scheme == "" || u.Scheme == "http" {
		if strings.IndexRune(u.Host, ':') == -1 {
			u.Host += ":80"
		}
		return func(network, addr string) (net.Conn, error) {
			connectReq := &http.Request{
				Method: "CONNECT",
				URL:    &url.URL{Opaque: addr},
				Host:   addr,
				Header: make(http.Header),
			}
			if connectReqHandler != nil {
				connectReqHandler(connectReq)
			}
			c, err := proxy.dial(network, u.Host)
			if err != nil {
				return nil, err
			}
			connectReq.Write(c)
			// Read response.
			// Okay to use and discard buffered reader here, because
			// TLS server will not speak until spoken to.
			br := bufio.NewReader(c)
			resp, err := http.ReadResponse(br, connectReq)
			if err != nil {
				c.Close()
				return nil, err
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				resp, err := ioutil.ReadAll(resp.Body)
				if err != nil {
					return nil, err
				}
				c.Close()
				return nil, errors.New("proxy refused connection" + string(resp))
			}
			return c, nil
		}
	}
	if u.Scheme == "https" {
		if strings.IndexRune(u.Host, ':') == -1 {
			u.Host += ":443"
		}
		return func(network, addr string) (net.Conn, error) {
			c, err := proxy.dial(network, u.Host)
			if err != nil {
				return nil, err
			}
			c = tls.Client(c, proxy.Tr.TLSClientConfig)
			connectReq := &http.Request{
				Method: "CONNECT",
				URL:    &url.URL{Opaque: addr},
				Host:   addr,
				Header: make(http.Header),
			}
			if connectReqHandler != nil {
				connectReqHandler(connectReq)
			}
			connectReq.Write(c)
			// Read response.
			// Okay to use and discard buffered reader here, because
			// TLS server will not speak until spoken to.
			br := bufio.NewReader(c)
			resp, err := http.ReadResponse(br, connectReq)
			if err != nil {
				c.Close()
				return nil, err
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				body, err := ioutil.ReadAll(io.LimitReader(resp.Body, 500))
				if err != nil {
					return nil, err
				}
				c.Close()
				return nil, errors.New("proxy refused connection" + string(body))
			}
			return c, nil
		}
	}
	return nil
}

func TLSConfigFromCA(ca *tls.Certificate) func(host string, ctx *ProxyCtx) (*tls.Config, error) {
	return func(host string, ctx *ProxyCtx) (*tls.Config, error) {
		var err error
		var cert *tls.Certificate

		hostname := stripPort(host)
		config := *defaultTLSConfig
		ctx.Logf("signing for %s", hostname)
		cert, err = ctx.signer(ca, []string{hostname})

		if cert == nil {
			if err != nil {
				ctx.Warnf("Cannot sign host certificate with provided CA: %s", err)
				return nil, err
			}
		}
		config.Certificates = append(config.Certificates, *cert)
		return &config, nil
	}
}
