package goproxy

import (
	"bufio"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
)

func headerContains(header http.Header, name string, value string) bool {
	for _, v := range header[name] {
		for _, s := range strings.Split(v, ",") {
			if strings.EqualFold(value, strings.TrimSpace(s)) {
				return true
			}
		}
	}
	return false
}

func isWebSocketRequest(r *http.Request) bool {
	return headerContains(r.Header, "Connection", "upgrade") &&
		headerContains(r.Header, "Upgrade", "websocket")
}

func (proxy *ProxyHttpServer) serveWebsocketTLS(
	ctx *ProxyCtx,
	req *http.Request,
	tlsConfig *tls.Config,
	clientConn *tls.Conn,
) {
	// wss
	host := req.URL.Host
	// Port is optional in req.URL.Host, in this case SplitHostPort returns
	// an error, and we add the default port
	_, port, err := net.SplitHostPort(req.URL.Host)
	if err != nil || port == "" {
		host = net.JoinHostPort(req.URL.Host, "443")
	}

	targetConn, err := proxy.connectDial(ctx, "tcp", host)
	if err != nil {
		ctx.Warnf("Error dialing target site: %v", err)
		return
	}
	defer targetConn.Close()

	// Add TLS to the raw TCP connection
	targetConn, err = proxy.initializeTLSconnection(ctx, targetConn, tlsConfig, host)
	if err != nil {
		ctx.Warnf("Websocket TLS connection error: %v", err)
		return
	}

	// Perform handshake
	if err := proxy.websocketHandshake(ctx, req, targetConn, clientConn); err != nil {
		ctx.Warnf("Websocket handshake error: %v", err)
		return
	}

	// Proxy wss connection
	proxy.proxyWebsocket(ctx, targetConn, clientConn)
}

func (proxy *ProxyHttpServer) hijackConnection(ctx *ProxyCtx, w http.ResponseWriter) (net.Conn, error) {
	// Connect to Client
	hj, ok := w.(http.Hijacker)
	if !ok {
		panic("httpserver does not support hijacking")
	}
	clientConn, _, err := hj.Hijack()
	if err != nil {
		ctx.Warnf("Hijack error: %v", err)
		return nil, err
	}
	return clientConn, nil
}

func (proxy *ProxyHttpServer) serveWebsocket(ctx *ProxyCtx, clientConn net.Conn, req *http.Request) {
	// ws
	host := req.URL.Host
	// Port is optional in req.URL.Host, in this case SplitHostPort returns
	// an error, and we add the default port
	_, port, err := net.SplitHostPort(req.URL.Host)
	if err != nil || port == "" {
		host = net.JoinHostPort(req.URL.Host, "80")
	}

	targetConn, err := proxy.connectDial(ctx, "tcp", host)
	if err != nil {
		ctx.Warnf("Error dialing target site: %v", err)
		return
	}
	defer targetConn.Close()

	// Perform handshake
	if err := proxy.websocketHandshake(ctx, req, targetConn, clientConn); err != nil {
		ctx.Warnf("Websocket handshake error: %v", err)
		return
	}

	// Proxy ws connection
	proxy.proxyWebsocket(ctx, targetConn, clientConn)
}

func (proxy *ProxyHttpServer) websocketHandshake(
	ctx *ProxyCtx,
	req *http.Request,
	targetSiteConn io.ReadWriter,
	clientConn io.ReadWriter,
) error {
	// write handshake request to target
	err := req.Write(targetSiteConn)
	if err != nil {
		ctx.Warnf("Error writing upgrade request: %v", err)
		return err
	}

	targetTLSReader := bufio.NewReader(targetSiteConn)

	// Read handshake response from target
	resp, err := http.ReadResponse(targetTLSReader, req)
	if err != nil {
		ctx.Warnf("Error reading handhsake response  %v", err)
		return err
	}

	// Run response through handlers
	resp = proxy.filterResponse(resp, ctx)

	// Proxy handshake back to client
	err = resp.Write(clientConn)
	if err != nil {
		ctx.Warnf("Error writing handshake response: %v", err)
		return err
	}
	return nil
}

func (proxy *ProxyHttpServer) proxyWebsocket(ctx *ProxyCtx, dest io.ReadWriter, source io.ReadWriter) {
	errChan := make(chan error, 2)
	cp := func(dst io.Writer, src io.Reader) {
		_, err := io.Copy(dst, src)
		if err != nil && !errors.Is(err, net.ErrClosed) {
			ctx.Warnf("Websocket error: %v", err)
		}
		errChan <- err
	}

	// Start proxying websocket data
	go cp(dest, source)
	go cp(source, dest)
	<-errChan
}
