package goproxy

import (
	"bufio"
	"bytes"
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

func (proxy *ProxyHttpServer) hijackConnection(ctx *ProxyCtx, w http.ResponseWriter) (net.Conn, *bufio.Reader, error) {
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
	// upgrade). The caller must relay them via bufferedClientReader.
	return clientConn, rw.Reader, nil
}

// bufferedClientReader returns a reader that first replays the bytes already
// buffered in br by the HTTP request parser, then continues from conn.
//
// A WebSocket client may send its first frame in the same write as the upgrade
// request. Those bytes are read off the socket into the parser's buffer, so a
// relay that reads only from conn would strand them.
//
// We deliberately replay a copy of the buffered bytes instead of reading through
// br itself: with PreventCanonicalization enabled, br wraps an io.TeeReader that
// retains every byte read from it, so relaying the whole session through br
// would grow that buffer unbounded for the connection's lifetime.
func bufferedClientReader(br *bufio.Reader, conn io.Reader) io.Reader {
	if br == nil {
		return conn
	}
	// Peek never advances the reader, and requesting Buffered() bytes always
	// succeeds, so this is a snapshot of the currently buffered bytes.
	buffered, _ := br.Peek(br.Buffered())
	if len(buffered) == 0 {
		return conn
	}
	return io.MultiReader(bytes.NewReader(buffered), conn)
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
