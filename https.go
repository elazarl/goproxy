package goproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
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
	if proxy.Tr.Dial == nil {
		return net.Dial(network, addr)
	}
	return proxy.Tr.Dial(network, addr)
}

func (proxy *ProxyHttpServer) connectDial(network, addr string) (c net.Conn, err error) {
	if proxy.ConnectDial == nil {
		return proxy.dial(network, addr)
	}
	return proxy.ConnectDial(network, addr)
}

func (proxy *ProxyHttpServer) dialContext(ctx *ProxyCtx, network, addr string) (c net.Conn, err error) {
	if proxy.Tr.DialContext == nil {
		return proxy.connectDial(network, addr)
	}
	pctx := context.WithValue(context.Background(), ProxyContextKey, ctx)
	return proxy.Tr.DialContext(pctx, network, addr)
}

func (proxy *ProxyHttpServer) connectDialContext(ctx *ProxyCtx, network, addr string) (c net.Conn, err error) {
	if proxy.ConnectDialContext == nil {
		return proxy.dialContext(ctx, network, addr)
	}
	return proxy.ConnectDialContext(ctx, network, addr)
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

	if proxy.ConnectClientConnHandler != nil {
		proxyClient = proxy.ConnectClientConnHandler(proxyClient)
	}

	ctx.Logf("Running %d CONNECT handlers", len(proxy.httpsHandlers))
	todo, host := OkConnect, r.URL.Host
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

		httpsProxy, err := httpsProxyFromEnv(r.URL)
		if err != nil {
			ctx.Warnf("Error configuring HTTPS proxy err=%q url=%q", err, r.URL.String())
		}

		var targetSiteCon net.Conn
		if httpsProxy == "" {
			targetSiteCon, err = proxy.connectDialContext(ctx, "tcp", host)
		} else {
			targetSiteCon, err = proxy.connectDialProxyWithContext(ctx, httpsProxy, host)
		}
		if err != nil {
			httpError(proxyClient, ctx, err)
			return
		}

		ctx.Logf("Accepting CONNECT to %s", host)
		proxyClient.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))

		if proxy.ConnectCopyHandler != nil {
			go proxy.ConnectCopyHandler(ctx, proxyClient, targetSiteCon)
			return
		}

		targetTCP, targetOK := targetSiteCon.(*net.TCPConn)
		proxyClientTCP, clientOK := proxyClient.(*net.TCPConn)
		if targetOK && clientOK {
			go copyAndClose(ctx, targetTCP, proxyClientTCP)
			go copyAndClose(ctx, proxyClientTCP, targetTCP)
		} else {
			// There is a race with the runtime here. In the case where the
			// connection to the target site times out, we cannot control which
			// io.Copy loop will receive the timeout signal first. This means
			// that in some cases the error passed to the ConnErrorHandler will
			// be the timeout error, and in other cases it will be an error raised
			// by the use of a closed network connection.
			//
			// 2020/05/28 23:42:17 [001] WARN: Error copying to client: read tcp 127.0.0.1:33742->127.0.0.1:34763: i/o timeout
			// 2020/05/28 23:42:17 [001] WARN: Error copying to client: read tcp 127.0.0.1:45145->127.0.0.1:60494: use of closed network connection
			//
			// It's also not possible to synchronize these connection closures due to
			// TCP connections which are half-closed. When this happens, only the one
			// side of the connection breaks out of its io.Copy loop. The other side
			// of the connection remains open until it either times out or is reset by
			// the client.
			go func() {
				err := copyOrWarn(ctx, targetSiteCon, proxyClient)
				if err != nil && ctx.ConnErrorHandler != nil && !isClosedNetworkConnError(err) {
					ctx.ConnErrorHandler(err)
				}
				targetSiteCon.Close()
			}()
			go func() {
				copyOrWarn(ctx, proxyClient, targetSiteCon)
				proxyClient.Close()
			}()
		}

	case ConnectHijack:
		ctx.Logf("Hijacking CONNECT to %s", host)
		proxyClient.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))
		todo.Hijack(r, proxyClient, ctx)
	case ConnectHTTPMitm:
		proxyClient.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))
		ctx.Logf("Assuming CONNECT is plain HTTP tunneling, mitm proxying it")
		targetSiteCon, err := proxy.connectDial("tcp", host)
		if err != nil {
			ctx.Warnf("Error dialing to %s: %s", host, err.Error())
			return
		}
		for {
			client := bufio.NewReader(proxyClient)
			remote := bufio.NewReader(targetSiteCon)
			req, err := http.ReadRequest(client)
			if err != nil && err != io.EOF {
				ctx.Warnf("cannot read request of MITM HTTP client: %+#v", err)
			}
			if err != nil {
				return
			}
			req, resp := proxy.filterRequest(req, ctx)
			if resp == nil {
				if err := req.Write(targetSiteCon); err != nil {
					httpError(proxyClient, ctx, err)
					return
				}
				resp, err = http.ReadResponse(remote, req)
				if err != nil {
					httpError(proxyClient, ctx, err)
					return
				}
				defer resp.Body.Close()
			}
			resp = proxy.filterResponse(resp, ctx)
			if err := resp.Write(proxyClient); err != nil {
				httpError(proxyClient, ctx, err)
				return
			}
		}
	case ConnectMitm:
		proxyClient.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))
		ctx.Logf("Assuming CONNECT is TLS, mitm proxying it")
		// this goes in a separate goroutine, so that the net/http server won't think we're
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
		go func() {
			//TODO: cache connections to the remote website
			rawClientTls := tls.Server(proxyClient, tlsConfig)
			if err := rawClientTls.Handshake(); err != nil {
				ctx.Warnf("Cannot handshake client %v %v", r.Host, err)
				return
			}
			defer rawClientTls.Close()
			clientTlsReader := bufio.NewReader(rawClientTls)
			for !isEof(clientTlsReader) {
				req, err := http.ReadRequest(clientTlsReader)
				// Set the RoundTripper on the ProxyCtx within the `HandleConnect` action of goproxy, then
				// inject the roundtripper here in order to use a custom round tripper while mitm.
				var ctx = &ProxyCtx{Req: req, Session: atomic.AddInt64(&proxy.sess, 1), proxy: proxy, UserData: ctx.UserData, RoundTripper: ctx.RoundTripper}
				if err != nil && err != io.EOF {
					return
				}
				if err != nil {
					ctx.Warnf("Cannot read TLS request from mitm'd client %v %v", r.Host, err)
					return
				}
				req.RemoteAddr = r.RemoteAddr // since we're converting the request, need to carry over the original connecting IP as well
				ctx.Logf("req %v", r.Host)

				if !httpsRegexp.MatchString(req.URL.String()) {
					req.URL, err = url.Parse("https://" + r.Host + req.URL.String())
				}

				// Bug fix which goproxy fails to provide request
				// information URL in the context when does HTTPS MITM
				ctx.Req = req

				req, resp := proxy.filterRequest(req, ctx)
				if resp == nil {
					if err != nil {
						ctx.Warnf("Illegal URL %s", "https://"+r.Host+req.URL.Path)
						return
					}
					removeProxyHeaders(ctx, req)
					resp, err = ctx.RoundTrip(req)
					if err != nil {
						ctx.Warnf("Cannot read TLS response from mitm'd server %v", err)
						return
					}
					ctx.Logf("resp %v", resp.Status)
				}
				resp = proxy.filterResponse(resp, ctx)
				defer resp.Body.Close()

				text := resp.Status
				statusCode := strconv.Itoa(resp.StatusCode) + " "
				if strings.HasPrefix(text, statusCode) {
					text = text[len(statusCode):]
				}
				// always use 1.1 to support chunked encoding
				if _, err := io.WriteString(rawClientTls, "HTTP/1.1"+" "+statusCode+text+"\r\n"); err != nil {
					ctx.Warnf("Cannot write TLS response HTTP status from mitm'd client: %v", err)
					return
				}
				// Since we don't know the length of resp, return chunked encoded response
				// TODO: use a more reasonable scheme
				resp.Header.Del("Content-Length")
				resp.Header.Set("Transfer-Encoding", "chunked")
				// Force connection close otherwise chrome will keep CONNECT tunnel open forever
				resp.Header.Set("Connection", "close")
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
	if ctx.HTTPErrorHandler != nil {
		ctx.HTTPErrorHandler(w, ctx, err)
		return
	}
	if _, err := io.WriteString(w, "HTTP/1.1 502 Bad Gateway\r\n\r\n"); err != nil {
		ctx.Warnf("Error responding to client: %s", err)
	}
	if err := w.Close(); err != nil {
		ctx.Warnf("Error closing client connection: %s", err)
	}
}

// isClosedNetworkConnError returns true if the error contains the suffix "use of closed network connection".
// This isn't ideal, and in Go 1.16 we will be able to check for net.ErrClosed.
func isClosedNetworkConnError(err error) bool {
	return strings.HasSuffix(err.Error(), "use of closed network connection")
}

func copyOrWarn(ctx *ProxyCtx, dst io.Writer, src io.Reader) error {
	_, err := io.Copy(dst, src)
	if err != nil && !isClosedNetworkConnError(err) {
		ctx.Warnf("Error copying to client: %s", err)
	}
	return err
}

func copyAndClose(ctx *ProxyCtx, dst, src *net.TCPConn) {
	if _, err := io.Copy(dst, src); err != nil {
		ctx.Warnf("Error copying to client: %s", err)
	}

	dst.CloseWrite()
	src.CloseRead()
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
				resp, err := ioutil.ReadAll(io.LimitReader(resp.Body, 500))
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
		config := defaultTLSConfig.Clone()
		ctx.Logf("signing for %s", stripPort(host))
		cert, err := signHost(*ca, []string{stripPort(host)})
		if err != nil {
			ctx.Warnf("Cannot sign host certificate with provided CA: %s", err)
			return nil, err
		}
		config.Certificates = append(config.Certificates, cert)
		return config, nil
	}
}

func (proxy *ProxyHttpServer) connectDialProxyWithContext(ctx *ProxyCtx, proxyHost, host string) (net.Conn, error) {
	proxyURL, err := url.Parse(proxyHost)
	if err != nil {
		return nil, err
	}

	c, err := proxy.connectDialContext(ctx, "tcp", proxyURL.Host)
	if err != nil {
		return nil, err
	}

	if proxyURL.Scheme == "https" {
		c = tls.Client(c, proxy.Tr.TLSClientConfig)
	}

	connectReq := &http.Request{
		Method: "CONNECT",
		URL:    &url.URL{Opaque: host},
		Host:   host,
		Header: make(http.Header),
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

// httpsProxyFromEnv allows goproxy to respect no_proxy env vars
// https://github.com/stripe/goproxy/pull/5
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

	return fmt.Sprintf("%s://%s:%s", proxyURL.Scheme, proxyURL.Hostname(), service), nil
}
