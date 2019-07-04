package goproxy

import (
	"bufio"
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
		targetSite, err := proxy.connectDial("tcp", host)
		if err != nil {
			ctx.Warnf("Error dialing to %s: %s", host, err.Error())
			return
		}

		proxyClient.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))
		ctx.Logf("Assuming CONNECT is plain HTTP tunneling, mitm proxying it")

		client := bufio.NewReader(proxyClient)
		remote := bufio.NewReader(targetSite)

		var (
			req  *http.Request
			resp *http.Response
		)

		for {
			// 1. read the request from the client
			req, err = http.ReadRequest(client)
			if err != nil {
				if err != io.EOF {
					ctx.Warnf("cannot read request of MITM HTTP client: %+#v", err)
				}
				return
			}

			// 2. filter the client request
			req, resp = proxy.filterRequest(req, ctx)
			if resp == nil {
				if err = req.Write(targetSite); err != nil {
					httpError(proxyClient, ctx, err)
					return
				}
				resp, err = http.ReadResponse(remote, req)
				if err != nil {
					httpError(proxyClient, ctx, err)
					return
				}
			}

			// 3. filter the response
			resp = proxy.filterResponse(resp, ctx)
			err = resp.Write(proxyClient)
			resp.Body.Close()

			if err != nil {
				httpError(proxyClient, ctx, err)
				return
			}
		}

	case ConnectMitm:
		// This goes in a separate goroutine, so that the net/http server won't think we're
		// still handling the request even after hijacking the connection. Those HTTP CONNECT
		// request can take forever, and the server will be stuck when "closed".
		// TODO: Allow Server.Close() mechanism to shut down this connection as nicely as possible
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
		ctx.Logf("Assuming CONNECT is TLS, mitm proxying it")

		go func() {
			rawClientTls := tls.Server(proxyClient, tlsConfig)
			if err := rawClientTls.Handshake(); err != nil {
				ctx.Warnf("Cannot handshake client %v %v", r.Host, err)
				return
			}
			defer rawClientTls.Close()

			clientTls := bufio.NewReader(rawClientTls)

			for {
				// 1. read the the request from the client.
				req, err := http.ReadRequest(clientTls)
				if err != nil {
					if err != io.EOF {
						ctx.Warnf("Cannot read TLS request from mitm'd client %v %v", r.Host, err)
						return
					}
					// EOF
					break
				} else if req == nil {
					ctx.Warnf("Empty request from mitm'd client")
					return
				}

				// 2. Setup a new ProxyCtx for the intercepted
				// stream.
				nctx := &ProxyCtx{
					Req:      req,
					Session:  atomic.AddInt64(&proxy.sess, 1),
					proxy:    proxy,
					UserData: ctx.UserData,
				}

				// Since we're converting the request, need to
				// carry over the original connecting IP as
				// well.
				req.RemoteAddr = r.RemoteAddr
				nctx.Logf("req %v", r.Host)

				if !httpsRegexp.MatchString(req.URL.String()) {
					req.URL, err = url.Parse("https://" + r.Host + req.URL.String())
					// err is handled below
				}

				// Put the original request from the client
				// into the context so is available at later
				// time.
				nctx.Req = req

				// 3. Filter the request.
				filreq, resp := proxy.filterRequest(req, nctx)
				if resp == nil {
					// err is from the call to url.Parse above
					if err != nil {
						nctx.Warnf("Illegal URL %s", "https://"+r.Host+filreq.URL.Path)
						return
					} else if filreq == nil {
						nctx.Warnf("Empty filtered request")
						return
					}

					// removeProxyHeaders(nctx, filreq)

					// Send the request to the target
					resp, err = nctx.RoundTrip(filreq)
					if err != nil {
						nctx.Warnf("Cannot read TLS response from mitm'd server %v", err)
						return
					}
					nctx.Logf("resp %v", resp.Status)
				}

				// 4. Filter the response.
				filtered := proxy.filterResponse(resp, nctx)

				// 5. Write the filtered response to the client
				err = filtered.Write(rawClientTls)
				resp.Body.Close()
				filtered.Body.Close()
				nctx.Warnf("wrote to rawClientTls: %v\n", filtered)
				if err != nil {
					nctx.Warnf("Failed to write response to client: %v", err)
					return
				}

				if req.Close {
					nctx.Warnf("Non-persistent connection; closing")
					return
				}
			}
			ctx.Logf("Exiting on EOF")
		}()

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

func copyOrWarn(ctx *ProxyCtx, dst io.Writer, src io.Reader, wg *sync.WaitGroup) {
	if _, err := io.Copy(dst, src); err != nil {
		ctx.Warnf("Error copying to client: %s", err)
	}
	wg.Done()
}

func copyAndClose(ctx *ProxyCtx, dst, src *net.TCPConn, wg *sync.WaitGroup) {
	if _, err := io.Copy(dst, src); err != nil {
		ctx.Warnf("Error copying to client: %s", err)
	}

	dst.CloseWrite()
	src.CloseRead()
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
