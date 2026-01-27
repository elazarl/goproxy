package goproxy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/inconshreveable/go-vhost"
)

// TransparentListener wraps a net.Listener to support transparent proxy mode.
// It automatically detects TLS connections and extracts the SNI to determine
// the target host, eliminating the need for explicit CONNECT requests.
type TransparentListener struct {
	net.Listener
	proxy *ProxyHttpServer
}

// NewTransparentListener creates a new TransparentListener that wraps the given listener.
func NewTransparentListener(ln net.Listener, proxy *ProxyHttpServer) *TransparentListener {
	return &TransparentListener{
		Listener: ln,
		proxy:    proxy,
	}
}

// Accept waits for and returns the next connection to the listener.
// For TLS connections, it peeks at the ClientHello to extract SNI.
func (tl *TransparentListener) Accept() (net.Conn, error) {
	conn, err := tl.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &transparentConn{Conn: conn, proxy: tl.proxy}, nil
}

// transparentConn wraps a net.Conn with transparent proxy detection capabilities.
type transparentConn struct {
	net.Conn
	proxy      *ProxyHttpServer
	peekedData []byte
	mu         sync.Mutex
}

// Read implements net.Conn.Read, returning any peeked data first.
func (tc *transparentConn) Read(b []byte) (int, error) {
	tc.mu.Lock()
	if len(tc.peekedData) > 0 {
		n := copy(b, tc.peekedData)
		tc.peekedData = tc.peekedData[n:]
		tc.mu.Unlock()
		return n, nil
	}
	tc.mu.Unlock()
	return tc.Conn.Read(b)
}

// transparentResponseWriter implements http.ResponseWriter for transparent proxy connections.
// It suppresses the "200 Connection Established" response that would normally be sent
// for CONNECT requests, since in transparent mode the client doesn't expect this.
type transparentResponseWriter struct {
	net.Conn
	headerWritten bool
}

func (trw *transparentResponseWriter) Header() http.Header {
	return make(http.Header)
}

func (trw *transparentResponseWriter) Write(buf []byte) (int, error) {
	// Suppress the HTTP 200 OK response that goproxy sends for CONNECT requests.
	// In transparent mode, the client doesn't expect this response.
	if !trw.headerWritten {
		trw.headerWritten = true
		if bytes.Equal(buf, []byte("HTTP/1.0 200 OK\r\n\r\n")) ||
			bytes.Equal(buf, []byte("HTTP/1.0 200 Connection established\r\n\r\n")) ||
			bytes.HasPrefix(buf, []byte("HTTP/1.0 200")) ||
			bytes.HasPrefix(buf, []byte("HTTP/1.1 200")) {
			// Check if this looks like a CONNECT response (short response with just status)
			if len(buf) < 100 && bytes.Contains(buf, []byte("\r\n\r\n")) {
				return len(buf), nil
			}
		}
	}
	return trw.Conn.Write(buf)
}

func (trw *transparentResponseWriter) WriteHeader(code int) {
	// No-op for transparent mode
}

func (trw *transparentResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return trw.Conn, bufio.NewReadWriter(bufio.NewReader(trw.Conn), bufio.NewWriter(trw.Conn)), nil
}

// isTLSHandshake checks if the given byte slice starts with a TLS handshake record.
// TLS records start with content type 0x16 (handshake) followed by version bytes.
func isTLSHandshake(data []byte) bool {
	if len(data) < 3 {
		return false
	}
	// TLS record: ContentType(1) | Version(2) | Length(2) | ...
	// ContentType 0x16 = Handshake
	// Version: 0x0301 (TLS 1.0), 0x0302 (TLS 1.1), 0x0303 (TLS 1.2/1.3)
	if data[0] != 0x16 {
		return false
	}
	// Check for valid TLS version
	if data[1] == 0x03 && data[2] <= 0x04 {
		return true
	}
	// Also accept SSL 3.0 (0x0300) for compatibility
	if data[1] == 0x03 && data[2] == 0x00 {
		return true
	}
	return false
}

// peekClientHello reads enough bytes to determine if this is a TLS connection
// and returns the peeked bytes along with whether it's TLS.
func peekClientHello(conn net.Conn) (peeked []byte, isTLS bool, err error) {
	// Set a reasonable timeout for the peek operation
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return nil, false, err
	}
	defer func() {
		// Clear deadline; if this fails, we still return the original result
		_ = conn.SetReadDeadline(time.Time{})
	}()

	// Read enough bytes to detect TLS (minimum 5 bytes for TLS record header)
	buf := make([]byte, 5)
	n, err := io.ReadFull(conn, buf)
	if err != nil {
		if n > 0 {
			return buf[:n], false, nil
		}
		return nil, false, err
	}

	return buf, isTLSHandshake(buf), nil
}

// prefixConn wraps a net.Conn and prepends data to be read first.
type prefixConn struct {
	net.Conn
	prefix []byte
	mu     sync.Mutex
}

func (pc *prefixConn) Read(b []byte) (int, error) {
	pc.mu.Lock()
	if len(pc.prefix) > 0 {
		n := copy(b, pc.prefix)
		pc.prefix = pc.prefix[n:]
		pc.mu.Unlock()
		return n, nil
	}
	pc.mu.Unlock()
	return pc.Conn.Read(b)
}

// HandleTransparentConnection handles a raw TCP connection in transparent proxy mode.
// It automatically detects whether the connection is TLS or plain HTTP and routes accordingly.
// For TLS connections, it extracts the SNI from the ClientHello.
// For HTTP connections, it parses the request and forwards it.
func (proxy *ProxyHttpServer) HandleTransparentConnection(conn net.Conn) {
	defer conn.Close()

	// Peek at the first few bytes to determine if this is TLS
	peeked, isTLS, err := peekClientHello(conn)
	if err != nil {
		proxy.Logger.Printf("Error peeking connection: %v", err)
		return
	}

	// Create a connection that includes the peeked bytes
	prefixed := &prefixConn{Conn: conn, prefix: peeked}

	if isTLS {
		proxy.handleTransparentTLS(prefixed)
	} else {
		proxy.handleTransparentHTTP(prefixed)
	}
}

// handleTransparentTLS handles a transparent TLS connection by extracting SNI
// and creating a synthetic CONNECT request.
func (proxy *ProxyHttpServer) handleTransparentTLS(conn net.Conn) {
	// Use go-vhost to extract SNI from the TLS ClientHello
	tlsConn, err := vhost.TLS(conn)
	if err != nil {
		proxy.Logger.Printf("Error parsing TLS ClientHello: %v", err)
		conn.Close()
		return
	}

	host := tlsConn.Host()
	if host == "" {
		proxy.Logger.Printf("Cannot support non-SNI enabled clients")
		tlsConn.Close()
		return
	}

	// Create a synthetic CONNECT request
	connectReq := &http.Request{
		Method: http.MethodConnect,
		URL: &url.URL{
			Opaque: host,
			Host:   net.JoinHostPort(host, "443"),
		},
		Host:       host,
		Header:     make(http.Header),
		RemoteAddr: conn.RemoteAddr().String(),
	}

	// Use our transparent response writer that suppresses the CONNECT response
	resp := &transparentResponseWriter{Conn: tlsConn}
	proxy.ServeHTTP(resp, connectReq)
}

// handleTransparentHTTP handles a transparent HTTP connection by parsing the request
// and forwarding it to the target server.
func (proxy *ProxyHttpServer) handleTransparentHTTP(conn net.Conn) {
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	for {
		req, err := http.ReadRequest(reader)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				proxy.Logger.Printf("Error reading HTTP request: %v", err)
			}
			return
		}

		// In transparent mode, the request URL might not have scheme/host
		// We need to construct it from the Host header
		if req.URL.Host == "" {
			req.URL.Host = req.Host
		}
		if req.URL.Scheme == "" {
			req.URL.Scheme = "http"
		}

		// Create a response writer that writes to the connection
		respWriter := &bufferedResponseWriter{
			conn:   conn,
			writer: writer,
			header: make(http.Header),
		}

		proxy.ServeHTTP(respWriter, req)

		if err := writer.Flush(); err != nil {
			return
		}

		// Check if we should keep the connection alive
		if req.Close || respWriter.shouldClose {
			return
		}
	}
}

// bufferedResponseWriter implements http.ResponseWriter for transparent HTTP connections.
type bufferedResponseWriter struct {
	conn        net.Conn
	writer      *bufio.Writer
	header      http.Header
	wroteHeader bool
	statusCode  int
	shouldClose bool
}

func (brw *bufferedResponseWriter) Header() http.Header {
	return brw.header
}

func (brw *bufferedResponseWriter) WriteHeader(code int) {
	if brw.wroteHeader {
		return
	}
	brw.wroteHeader = true
	brw.statusCode = code

	// Write status line
	statusText := http.StatusText(code)
	if statusText == "" {
		statusText = "Unknown"
	}
	// Write the status line using fmt.Fprintf for cleaner error handling
	_, _ = fmt.Fprintf(brw.writer, "HTTP/1.1 %d %s\r\n", code, statusText)

	// Write headers
	for key, values := range brw.header {
		for _, value := range values {
			_, _ = fmt.Fprintf(brw.writer, "%s: %s\r\n", key, value)
		}
	}
	_, _ = brw.writer.WriteString("\r\n")
}

func (brw *bufferedResponseWriter) Write(data []byte) (int, error) {
	if !brw.wroteHeader {
		brw.WriteHeader(http.StatusOK)
	}
	return brw.writer.Write(data)
}

func (brw *bufferedResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return brw.conn, bufio.NewReadWriter(bufio.NewReader(brw.conn), brw.writer), nil
}

// ListenAndServeTransparent starts the proxy server in transparent mode.
// It listens on the given address and handles both HTTP and HTTPS connections
// transparently, without requiring explicit proxy configuration from clients.
func (proxy *ProxyHttpServer) ListenAndServeTransparent(addr string) error {
	return proxy.ListenAndServeTransparentContext(context.Background(), addr)
}

// ListenAndServeTransparentContext is like ListenAndServeTransparent but accepts a context.
func (proxy *ProxyHttpServer) ListenAndServeTransparentContext(ctx context.Context, addr string) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return proxy.ServeTransparent(ln)
}

// ServeTransparent accepts connections on the listener and handles them in transparent mode.
// This is useful when you want to provide your own listener (e.g., for TLS or Unix sockets).
func (proxy *ProxyHttpServer) ServeTransparent(ln net.Listener) error {
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			// Check if the listener was closed (permanent error)
			var ne net.Error
			if errors.As(err, &ne) && !ne.Temporary() { //nolint:staticcheck // Temporary is deprecated but still useful
				return err
			}
			proxy.Logger.Printf("Error accepting connection: %v", err)
			continue
		}

		go proxy.HandleTransparentConnection(conn)
	}
}

// ListenAndServeTransparentTLS starts the proxy server in transparent mode for TLS-only connections.
// Use this when you have a dedicated port for HTTPS traffic (e.g., port 443 redirected via iptables).
func (proxy *ProxyHttpServer) ListenAndServeTransparentTLS(addr string) error {
	return proxy.ListenAndServeTransparentTLSContext(context.Background(), addr)
}

// ListenAndServeTransparentTLSContext is like ListenAndServeTransparentTLS but accepts a context.
func (proxy *ProxyHttpServer) ListenAndServeTransparentTLSContext(ctx context.Context, addr string) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return proxy.ServeTransparentTLS(ln)
}

// ServeTransparentTLS accepts connections on the listener and handles them as TLS in transparent mode.
// This skips the protocol detection and assumes all connections are TLS.
func (proxy *ProxyHttpServer) ServeTransparentTLS(ln net.Listener) error {
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && !ne.Temporary() { //nolint:staticcheck // Temporary is deprecated but still useful
				return err
			}
			proxy.Logger.Printf("Error accepting connection: %v", err)
			continue
		}

		go proxy.handleTransparentTLSConnection(conn)
	}
}

// handleTransparentTLSConnection handles a connection that is known to be TLS.
func (proxy *ProxyHttpServer) handleTransparentTLSConnection(conn net.Conn) {
	defer conn.Close()
	proxy.handleTransparentTLS(conn)
}

// ListenAndServeTransparentDual starts the proxy server with separate listeners for HTTP and HTTPS.
// This is useful when you want to redirect HTTP (port 80) and HTTPS (port 443) to different ports.
func (proxy *ProxyHttpServer) ListenAndServeTransparentDual(httpAddr, httpsAddr string) error {
	return proxy.ListenAndServeTransparentDualContext(context.Background(), httpAddr, httpsAddr)
}

// ListenAndServeTransparentDualContext is like ListenAndServeTransparentDual but accepts a context.
func (proxy *ProxyHttpServer) ListenAndServeTransparentDualContext(
	ctx context.Context, httpAddr, httpsAddr string,
) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var lc net.ListenConfig
	errCh := make(chan error, 2)

	// Start HTTP listener
	go func() {
		ln, err := lc.Listen(ctx, "tcp", httpAddr)
		if err != nil {
			errCh <- err
			return
		}
		errCh <- proxy.serveTransparentHTTP(ctx, ln)
	}()

	// Start HTTPS listener
	go func() {
		ln, err := lc.Listen(ctx, "tcp", httpsAddr)
		if err != nil {
			errCh <- err
			return
		}
		errCh <- proxy.ServeTransparentTLS(ln)
	}()

	// Wait for first error
	return <-errCh
}

// serveTransparentHTTP serves HTTP-only transparent connections.
func (proxy *ProxyHttpServer) serveTransparentHTTP(ctx context.Context, ln net.Listener) error {
	defer ln.Close()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		conn, err := ln.Accept()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && !ne.Temporary() { //nolint:staticcheck // Temporary is deprecated but still useful
				return err
			}
			proxy.Logger.Printf("Error accepting connection: %v", err)
			continue
		}

		go func(c net.Conn) {
			defer c.Close()
			proxy.handleTransparentHTTP(c)
		}(conn)
	}
}
