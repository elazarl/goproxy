package goproxy

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
)

var errCopyDone = errors.New("hijacked connection copy complete")

type closeWriter interface {
	CloseWrite() error
}

func headerContains(header http.Header, name string, value string) bool {
	for key, values := range header {
		if !strings.EqualFold(key, name) {
			continue
		}
		for _, v := range values {
			for _, s := range strings.Split(v, ",") {
				if strings.EqualFold(value, strings.TrimSpace(s)) {
					return true
				}
			}
		}
	}
	return false
}

func hasHeader(header http.Header, name string) bool {
	for key, values := range header {
		if !strings.EqualFold(key, name) {
			continue
		}
		for _, v := range values {
			if strings.TrimSpace(v) != "" {
				return true
			}
		}
	}
	return false
}

func isProtocolSwitchHeader(header http.Header) bool {
	return hasHeader(header, "Upgrade") &&
		headerContains(header, "Connection", "Upgrade")
}

func isProtocolSwitchResponse(resp *http.Response) bool {
	return resp != nil &&
		resp.StatusCode == http.StatusSwitchingProtocols &&
		isProtocolSwitchHeader(resp.Header)
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
// A client may send its first tunnel or upgrade bytes (a TLS ClientHello, an
// HTTP/2 preface, or a WebSocket frame) in the same write as the CONNECT or
// upgrade request. Those bytes are read off the socket into the parser's
// buffer, so a relay that reads only from conn would strand them. Both the
// CONNECT tunnel path and the MITM path use this helper.
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
func (proxy *ProxyHttpServer) proxyWebsocket(
	ctx *ProxyCtx,
	remoteConn io.ReadWriter,
	clientReader io.Reader,
	clientWriter io.Writer,
) {
	// Mirror net/http/httputil.ReverseProxy's switchProtocolCopier: propagate
	// CloseWrite on EOF and wait for the reverse direction when supported.
	errc := make(chan error, 2)
	go func() {
		if err := copyOrWarn(ctx, remoteConn, clientReader); err != nil {
			errc <- err
			return
		}
		if cw, ok := remoteConn.(closeWriter); ok {
			errc <- cw.CloseWrite()
			return
		}
		errc <- errCopyDone
	}()

	go func() {
		if err := copyOrWarn(ctx, clientWriter, remoteConn); err != nil {
			errc <- err
			return
		}
		if cw, ok := clientWriter.(closeWriter); ok {
			errc <- cw.CloseWrite()
			return
		}
		errc <- errCopyDone
	}()

	if err := <-errc; err == nil {
		<-errc
	}
}
