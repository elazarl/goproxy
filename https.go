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
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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

func (proxy *ProxyHttpServer) handleHttpsConnectAccept(ctx *ProxyCtx, host string, proxyClient net.Conn) {

	if !hasPort.MatchString(host) {
		host += ":80"
	}
	var targetSiteCon net.Conn
	var err error
	var logHeaders http.Header

	ctx.Logf("client type: %+v", reflect.TypeOf(proxyClient))
	ctx.Logf("client info: %s -> %s", proxyClient.LocalAddr().String(), proxyClient.RemoteAddr().String())

	//check for idle override
	var idleTimeout time.Duration
	if ctx.IdleConnTimeout != 0 {
		idleTimeout = ctx.IdleConnTimeout
	} else {
		idleTimeout = 90 * time.Second
	}

	sendHTTPOK := false
	setTargetKA := true

	if ctx.ForwardProxy != "" {

		if ctx.ForwardProxyProto == "" {
			ctx.ForwardProxyProto = "http"
		}

		if ctx.ForwardProxyProto == "https" {
			setTargetKA = false
		}

		ctx.Logf("dial via forward proxy: %v %+v", ctx.ForwardProxyProto, ctx.ForwardProxy)

		tr := &http.Transport{
			MaxIdleConns:          ctx.MaxIdleConns,
			MaxIdleConnsPerHost:   ctx.MaxIdleConnsPerHost,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			IdleConnTimeout:       idleTimeout,
			Proxy: func(req *http.Request) (*url.URL, error) {
				return url.Parse(ctx.ForwardProxyProto + "://" + ctx.ForwardProxy)
			},
			Dial: ctx.Proxy.NewConnectDialWithKeepAlives(ctx, ctx.ForwardProxyProto+"://"+ctx.ForwardProxy, func(req *http.Request) {
				if ctx.ForwardProxyAuth != "" {
					req.Header.Set("Proxy-Authorization", fmt.Sprintf("Basic %s", ctx.ForwardProxyAuth))
				}
				if len(ctx.ForwardProxyHeaders) > 0 {
					for _, pxyHeader := range ctx.ForwardProxyHeaders {
						ctx.Logf("setting proxy header %+v", pxyHeader)
						req.Header.Set(pxyHeader.Header, pxyHeader.Value)
					}
				}
				logHeaders = req.Header
			}),
		}

		if ctx.ForwardProxyFallbackTimeout > 0 {
			ctx.Logf("forward proxt fallback timeout set %+v", ctx.ForwardProxyFallbackTimeout)
			tr.DialContext = (&net.Dialer{
				Timeout:   time.Duration(int64(ctx.ForwardProxyFallbackTimeout)) * time.Second,
				KeepAlive: 30 * time.Second,
				DualStack: true,
			}).DialContext
			if ctx.ForwardProxyFallbackSecondaryTimeout > 0 {
				ctx.ForwardProxyFallbackTimeout = ctx.ForwardProxyFallbackSecondaryTimeout
			} else {
				ctx.ForwardProxyFallbackTimeout = 10
			}
		}

		dialStart := time.Now().UnixNano()

		targetSiteCon, err = tr.Dial("tcp", host)

		dialEnd := time.Now().UnixNano()

		if ctx.ForwardMetricsCounters.TLSTimes != nil {
			tlsTime := float64(dialEnd/1000000) - float64(dialStart/1000000)
			metric := *ctx.ForwardMetricsCounters.TLSTimes
			metric.Observe(float64(tlsTime))
		}

	} else if ctx.ForwardProxyDirect && ctx.ForwardProxySourceIP != "" {

		ctx.Logf("dial locally from: %+v", ctx.ForwardProxySourceIP)

		// dont use a proxy and use specific source IP
		tr := &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			Dial: func(network, address string) (net.Conn, error) {
				localAddr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("%s:0", ctx.ForwardProxySourceIP))
				if err != nil {
					ctx.Logf("Failed to resolve local address: %s - err: %v", ctx.ForwardProxySourceIP, err)
					return nil, err
				}
				d := net.Dialer{
					Timeout:   15 * time.Second,
					LocalAddr: localAddr,
				}
				return d.Dial("tcp", address)
			},
			MaxIdleConns:          ctx.MaxIdleConns,
			MaxIdleConnsPerHost:   ctx.MaxIdleConnsPerHost,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			IdleConnTimeout:       idleTimeout,
		}

		targetSiteCon, err = tr.Dial("tcp", host)

	} else if ctx.ForwardProxyTProxy {

		ctx.Logf("dialing via TPROXY from: %s -> %s", ctx.ForwardProxySourceIP, proxyClient.LocalAddr().String())

		tcpLocal, errTCP := net.ResolveTCPAddr("tcp", fmt.Sprintf("%s:0", ctx.ForwardProxySourceIP))
		if errTCP != nil {
			ctx.Logf("Failed to resolve local TCP address: %s - err: %v", ctx.ForwardProxySourceIP, errTCP)
		}
		tcpRemote, errTCP := net.ResolveTCPAddr("tcp", proxyClient.LocalAddr().String())
		if errTCP != nil {
			ctx.Logf("Failed to resolve remote TCP address: %s - err: %v", proxyClient.LocalAddr().String(), errTCP)
		}

		if strings.HasPrefix(proxyClient.LocalAddr().String(), ctx.ForwatdTProxyDropIP) {
			err = errors.New("cannot dial self")
		} else {
			if errTCP == nil && tcpRemote != nil {
				d := net.Dialer{
					Timeout:   10 * time.Second,
					LocalAddr: tcpLocal,
				}
				targetSiteCon, err = d.Dial("tcp", proxyClient.LocalAddr().String())
				// targetSiteCon, err = net.DialTCP("tcp", tcpLocal, tcpRemote)
			} else {
				err = errTCP
			}
		}

	} else {
		targetSiteCon, err = proxy.connectDial("tcp", host)
		sendHTTPOK = true
	}

	if err != nil {

		//handle tproxy errors
		if ctx.ForwardProxy == "" && ctx.ForwardProxyTProxy {
			ctx.Logf("error-metric: https (tproxy dial) to host: %s failed: %v - headers %+v", host, err, logHeaders)
			ctx.ForwardProxy = "127.0.0.1"
			ctx.SetErrorMetric()
			httpError(proxyClient, ctx, err)
			return
		}

		dnsCheck, _ := net.LookupHost(strings.Split(host, ":")[0])
		if len(dnsCheck) > 0 {
			ctx.Logf("error-metric: https to host: %s failed: %v - headers %+v", host, err, logHeaders)
			ctx.SetErrorMetric()
			// if a fallback func was provided, retry.
			// Since the ctx is created in this method, we just rerun handleHttps,
			// which will call any handlers again and setup the context with a new forward proxy
			if ctx.ForwardProxyErrorFallback != nil {
				todo := OkConnect
				for i, h := range proxy.httpsHandlers {
					newtodo, newhost := h.HandleConnect(host, ctx)
					// If found a result, break the loop immediately
					if newtodo != nil {
						todo, host = newtodo, newhost
						ctx.Logf("RETRY on %dth handler: %v %s", i, todo, host)
						break
					}
				}
				ctx.ForwardProxyErrorFallback = nil
				if todo.Action == ConnectAccept {
					ctx.Logf("RETRY forward proxy: ", ctx.ForwardProxy)
					proxy.handleHttpsConnectAccept(ctx, host, proxyClient)
					return
				}
			}
		}
		httpError(proxyClient, ctx, err)
		return
	}

	// only send HTTP OK if this is not a transparent proxy request
	if sendHTTPOK {
		proxyClient.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))
	}

	ctx.Logf("targetSiteCon type: %+v", reflect.TypeOf(targetSiteCon))
	ctx.Logf("targetSiteCon info: %s -> %s", targetSiteCon.LocalAddr().String(), targetSiteCon.RemoteAddr().String())

	//This is a hack for now to support tproxy metrics
	if ctx.ForwardProxy == "" && ctx.ForwardProxyTProxy {
		ctx.ForwardProxy = "127.0.0.1"
	}

	ctx.SetSuccessMetric()
	ctx.Logf("Accepting CONNECT to %s", host)

	//set tcp keep alives.
	tcpKAPeriod := 5
	if ctx.TCPKeepAlivePeriod > 0 {
		tcpKAPeriod = ctx.TCPKeepAlivePeriod
	}
	tcpKACount := 3
	if ctx.TCPKeepAliveCount > 0 {
		tcpKACount = ctx.TCPKeepAliveCount
	}
	tcpKAInterval := 3
	if ctx.TCPKeepAliveInterval > 0 {
		tcpKAInterval = ctx.TCPKeepAliveInterval
	}

	clientConn := &ProxyTCPConn{
		Conn:                 proxyClient,
		Logger:               ctx.ProxyLogger,
		ReadTimeout:          time.Second * 5,
		WriteTimeout:         time.Second * 5,
		IgnoreDeadlineErrors: true,
	}
	kaErr := clientConn.SetKeepaliveParameters(true, tcpKACount, tcpKAInterval, tcpKAPeriod)
	if kaErr != nil {
		ctx.Logf("clientConn KeepAlive error: %v", kaErr)
		clientConn.ReadTimeout = time.Second * time.Duration(ctx.ProxyReadDeadline)
		clientConn.WriteTimeout = time.Second * time.Duration(ctx.ProxyWriteDeadline)
		clientConn.IgnoreDeadlineErrors = false
	}

	targetConn := &ProxyTCPConn{
		Conn:                 targetSiteCon,
		Logger:               ctx.ProxyLogger,
		ReadTimeout:          time.Second * 5,
		WriteTimeout:         time.Second * 5,
		IgnoreDeadlineErrors: true,
	}
	// Since we dont have access to the *tls.Conn underlying connection, we have to set it
	// during the connectDial to proxy
	if setTargetKA {
		kaErr = targetConn.SetKeepaliveParameters(false, tcpKACount, tcpKAInterval, tcpKAPeriod)
		if kaErr != nil {
			ctx.Logf("targetConn KeepAlive error: %v", kaErr)
			targetConn.ReadTimeout = time.Second * time.Duration(ctx.ProxyReadDeadline)
			targetConn.WriteTimeout = time.Second * time.Duration(ctx.ProxyWriteDeadline)
			targetConn.IgnoreDeadlineErrors = false
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	cancelCtx, cancel := context.WithCancel(context.Background())
	go copyAndClose(cancelCtx, cancel, ctx, targetConn, clientConn, "sent", &wg)
	go copyAndClose(cancelCtx, cancel, ctx, clientConn, targetConn, "recv", &wg)
	wg.Wait()
	if ctx.ForwardMetricsCounters.ProxyBandwidth != nil {
		metric := *ctx.ForwardMetricsCounters.ProxyBandwidth
		metric.Add(float64(targetConn.BytesWrote + targetConn.BytesRead))
	}
	targetConn.Conn.Close()
	clientConn.Conn.Close()
	if ctx.Tail != nil {
		ctx.Tail(ctx)
	}
}

func (proxy *ProxyHttpServer) HandleHttps(w http.ResponseWriter, r *http.Request, conn *net.Conn) {

	ctx := &ProxyCtx{Req: r, Session: atomic.AddInt64(&proxy.sess, 1), Proxy: proxy, certStore: proxy.CertStore}

	var proxyClient net.Conn

	//if connection not provided, attempt highjack
	if conn == nil {
		hij, ok := w.(http.Hijacker)
		if !ok {
			panic("httpserver does not support hijacking")
		}
		var e error
		proxyClient, _, e = hij.Hijack()
		if e != nil {
			panic("Cannot hijack connection " + e.Error())
		}
	} else {
		proxyClient = *conn
		ctx.Logf("using provided proxyClient: %v, type %v", proxyClient, reflect.TypeOf(proxyClient))
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

		proxy.handleHttpsConnectAccept(ctx, host, proxyClient)

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
				var ctx = &ProxyCtx{Req: req, Session: atomic.AddInt64(&proxy.sess, 1), Proxy: proxy, UserData: ctx.UserData}
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

func copyAndClose(ctx context.Context, cancel context.CancelFunc, proxyCtx *ProxyCtx, dst, src *ProxyTCPConn, dir string, wg *sync.WaitGroup) {
	defer cancel()
	defer wg.Done()

	size := 32 * 1024
	if proxyCtx.CopyBufferSize > 0 {
		size = proxyCtx.CopyBufferSize * 1024
	}

	buf := make([]byte, size)
	var written int64
	var err error

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		nr, er := src.Read(buf)
		select {
		case <-ctx.Done():
			return
		default:
		}
		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			if netErr, ok := er.(net.Error); ok && netErr.Timeout() && src.IgnoreDeadlineErrors {
				continue
			}
			if er != io.EOF {
				err = er
			}
			break
		}
	}
	if err != nil {
		proxyCtx.Warnf("Error copying: %s", err)
	}

	switch dir {
	case "sent":
		proxyCtx.BytesSent = written
	case "recv":
		proxyCtx.BytesReceived = written
	}
}

func copyWithBuffer(dst io.Writer, src io.Reader, size int) (written int64, err error) {
	// If the reader has a WriteTo method, use it to do the copy.
	// Avoids an allocation and a copy.
	// if wt, ok := src.(io.WriterTo); ok {
	// 	return wt.WriteTo(dst)
	// }
	// // Similarly, if the writer has a ReadFrom method, use it to do the copy.
	// if rt, ok := dst.(io.ReaderFrom); ok {
	// 	return rt.ReadFrom(src)
	// }
	buf := make([]byte, size)

	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			if er != io.EOF {
				err = er
			}
			break
		}
	}
	return written, err
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

func (proxy *ProxyHttpServer) NewConnectDialWithKeepAlives(ctx *ProxyCtx, https_proxy string, connectReqHandler func(req *http.Request)) func(network, addr string) (net.Conn, error) {
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
				ctx.Logf("connect dial got error reponse: %v", resp.StatusCode)
				resp, err := ioutil.ReadAll(resp.Body)
				if err != nil {
					ctx.Logf("connect dial error read body: %v", err)
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

		//set tcp keep alives. TODO: make these defaults smaller for forward proxied requests
		tcpKAPeriod := 5
		if ctx.TCPKeepAlivePeriod > 0 {
			tcpKAPeriod = ctx.TCPKeepAlivePeriod
		}
		tcpKACount := 3
		if ctx.TCPKeepAliveCount > 0 {
			tcpKACount = ctx.TCPKeepAliveCount
		}
		tcpKAInterval := 3
		if ctx.TCPKeepAliveInterval > 0 {
			tcpKAInterval = ctx.TCPKeepAliveInterval
		}

		return func(network, addr string) (net.Conn, error) {
			c, err := proxy.dial(network, u.Host)
			if err != nil {
				return nil, err
			}
			// set the tcp keepalives before we create the tls.Conn since we lose access to the
			// underlying connection.
			targetConn := &ProxyTCPConn{
				Conn:                 c,
				Logger:               ctx.ProxyLogger,
				ReadTimeout:          time.Second * 5,
				WriteTimeout:         time.Second * 5,
				IgnoreDeadlineErrors: true,
			}
			kaErr := targetConn.SetKeepaliveParameters(false, tcpKACount, tcpKAInterval, tcpKAPeriod)
			if kaErr != nil {
				ctx.Logf("targetConn KeepAlive error: %v", kaErr)
				targetConn.ReadTimeout = time.Second * time.Duration(ctx.ProxyReadDeadline)
				targetConn.WriteTimeout = time.Second * time.Duration(ctx.ProxyWriteDeadline)
				targetConn.IgnoreDeadlineErrors = false
			}
			c = tls.Client(targetConn, proxy.Tr.TLSClientConfig)
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
				ctx.Logf("connect dial got error reponse: %v", resp.StatusCode)
				body, err := ioutil.ReadAll(io.LimitReader(resp.Body, 500))
				if err != nil {
					ctx.Logf("connect dial error read body: %v", err)
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
		ctx.Logf("signing for %s", stripPort(host))

		genCert := func() (*tls.Certificate, error) {
			return signHost(*ca, []string{hostname})
		}
		if ctx.certStore != nil {
			cert, err = ctx.certStore.Fetch(hostname, genCert)
		} else {
			cert, err = genCert()
		}

		if err != nil {
			ctx.Warnf("Cannot sign host certificate with provided CA: %s", err)
			return nil, err
		}

		config.Certificates = append(config.Certificates, *cert)
		return &config, nil
	}
}
