package goproxy

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

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

// hasHeader reports whether header contains a non-empty value for name. It
// iterates over keys instead of using header.Values so it also works with
// PreventCanonicalization, which leaves header keys in their original case.
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

// writeProtocolSwitchHead writes the head of a 101 response. http.Response.Write
// synthesizes "Content-Length: 0" for bodiless POST/PUT/PATCH responses, which
// RFC 9110 §8.6 forbids on 1xx responses, so the head is written directly.
func writeProtocolSwitchHead(w io.Writer, resp *http.Response) error {
	resp.Header.Del("Content-Length")
	resp.Header.Del("Transfer-Encoding")
	resp.Header.Del("Trailer")
	if _, err := fmt.Fprintf(w, "HTTP/1.1 %03d %s\r\n", resp.StatusCode, http.StatusText(resp.StatusCode)); err != nil {
		return err
	}
	if err := resp.Header.Write(w); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\r\n")
	return err
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

// proxyUpgradedConn relays bytes between the client and the upstream server
// until both directions have finished. When one direction reaches EOF it
// half-closes the opposite write side so the peer observes the end of stream,
// then keeps relaying the other direction. This is required for generic TCP
// upgrades, where a client may half-close its write side and still expect a
// response. The client read and write sides are passed separately so the
// caller can hand over a reader that still holds bytes buffered while parsing
// the upgrade request (e.g. a client frame that arrived in the same write).
func (proxy *ProxyHttpServer) proxyUpgradedConn(
	ctx *ProxyCtx,
	remoteConn io.ReadWriter,
	clientReader io.Reader,
	clientWriter io.Writer,
) {
	var wg sync.WaitGroup
	wg.Add(2)

	// remoteConn (the 101 response body) and clientWriter both implement
	// io.Closer. Use them to unblock the opposite direction on copy errors.
	remoteCloser, _ := remoteConn.(io.Closer)
	clientCloser, _ := clientWriter.(io.Closer)

	go func() {
		defer wg.Done()
		relayCopy(ctx, remoteConn, clientReader, remoteCloser, clientCloser)
	}()

	go func() {
		defer wg.Done()
		relayCopy(ctx, clientWriter, remoteConn, clientCloser, remoteCloser)
	}()

	wg.Wait()
}

// relayCopy copies src to dst. On a clean EOF it half-closes dst so the peer
// observes the end of stream while the reverse direction keeps flowing; on a
// copy error it fully closes both endpoints to unblock the reverse copy.
func relayCopy(ctx *ProxyCtx, dst io.Writer, src io.Reader, dstCloser, srcCloser io.Closer) {
	if err := copyOrWarn(ctx, dst, src); err != nil {
		if dstCloser != nil {
			_ = dstCloser.Close()
		}
		if srcCloser != nil {
			_ = srcCloser.Close()
		}
		return
	}
	if cw, ok := dst.(closeWriter); ok {
		_ = cw.CloseWrite()
	}
}
