package goproxy

import (
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

func isWebSocketHandshake(header http.Header) bool {
	return headerContains(header, "Connection", "Upgrade") &&
		headerContains(header, "Upgrade", "websocket")
}

func (proxy *ProxyHttpServer) hijackConnection(ctx *ProxyCtx, w http.ResponseWriter) (net.Conn, io.Reader, error) {
	// Connect to Client
	hj, ok := w.(http.Hijacker)
	if !ok {
		panic("httpserver does not support hijacking")
	}
	clientConn, rw, err := hj.Hijack()
	if err != nil {
		ctx.Warnf("Hijack error: %v", err)
		return nil, nil, err
	}
	// rw.Reader still holds any bytes the HTTP server buffered while parsing the
	// request (e.g. the first WebSocket frame sent in the same write as the
	// upgrade). Read through it before the raw connection, or those bytes are lost.
	return clientConn, rw.Reader, nil
}

// proxyWebsocket relays frames between the client and the upstream server until
// one side closes. The client read and write sides are passed separately so the
// caller can hand over a reader that still holds bytes buffered while parsing
// the upgrade request (e.g. a client frame that arrived in the same write).
func (proxy *ProxyHttpServer) proxyWebsocket(ctx *ProxyCtx, remoteConn io.ReadWriter, clientReader io.Reader, clientWriter io.Writer) {
	// 2 is the number of goroutines, this code is implemented according to
	// https://stackoverflow.com/questions/52031332/wait-for-one-goroutine-to-finish
	waitChan := make(chan struct{}, 2)
	go func() {
		_ = copyOrWarn(ctx, remoteConn, clientReader)
		waitChan <- struct{}{}
	}()

	go func() {
		_ = copyOrWarn(ctx, clientWriter, remoteConn)
		waitChan <- struct{}{}
	}()

	// Wait until one end closes the connection
	<-waitChan
}
