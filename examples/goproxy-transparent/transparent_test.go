package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elazarl/goproxy"
)

func TestTransparentHTTP(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hello" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("X-Transparent") != "yes" {
			t.Errorf("expected X-Transparent header, got %q", r.Header.Get("X-Transparent"))
		}
		_, _ = w.Write([]byte("http-ok"))
	}))
	defer backend.Close()

	proxy := newTransparentProxy(false)
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		req.Header.Set("X-Transparent", "yes")
		return req, nil
	})

	proxySrv := httptest.NewServer(proxy)
	defer proxySrv.Close()

	// Send an origin-form request directly to the proxy listener (like iptables DNAT).
	backendHost := strings.TrimPrefix(backend.URL, "http://")
	proxyAddr := strings.TrimPrefix(proxySrv.URL, "http://")
	conn, err := net.DialTimeout("tcp", proxyAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	rawReq := "GET /hello HTTP/1.1\r\nHost: " + backendHost + "\r\nConnection: close\r\n\r\n"
	if _, err := io.WriteString(conn, rawReq); err != nil {
		t.Fatalf("write request: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "http-ok" {
		t.Fatalf("got status=%d body=%q, want 200 \"http-ok\"", resp.StatusCode, string(body))
	}
}

func TestTransparentHTTPMissingHost(t *testing.T) {
	proxy := newTransparentProxy(false)
	proxySrv := httptest.NewServer(proxy)
	defer proxySrv.Close()

	proxyAddr := strings.TrimPrefix(proxySrv.URL, "http://")
	conn, err := net.DialTimeout("tcp", proxyAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	if _, err := io.WriteString(conn, "GET / HTTP/1.0\r\n\r\n"); err != nil {
		t.Fatalf("write request: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "Cannot handle requests without Host header") {
		t.Fatalf("unexpected body: %q", string(body))
	}
}

func TestTransparentHTTPS(t *testing.T) {
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/secret" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("X-Mitm") != "applied" {
			t.Errorf("expected X-Mitm header, got %q", r.Header.Get("X-Mitm"))
		}
		_, _ = w.Write([]byte("https-mitm-ok"))
	}))
	defer backend.Close()
	backendAddr := strings.TrimPrefix(backend.URL, "https://")

	proxy := newTransparentProxy(false)
	proxy.Tr = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var d net.Dialer
			if addr == "example.com:443" {
				return d.DialContext(ctx, network, backendAddr)
			}
			return d.DialContext(ctx, network, addr)
		},
	}
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		req.Header.Set("X-Mitm", "applied")
		return req, nil
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go handleTLSConn(c, proxy)
		}
	}()

	caCert, err := x509.ParseCertificate(goproxy.GoproxyCa.Certificate[0])
	if err != nil {
		t.Fatalf("parse goproxy CA: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)

	tlsConn, err := tls.DialWithDialer(
		&net.Dialer{Timeout: 5 * time.Second},
		"tcp",
		ln.Addr().String(),
		&tls.Config{
			ServerName: "example.com",
			RootCAs:    roots,
		},
	)
	if err != nil {
		t.Fatalf("tls dial transparent proxy: %v", err)
	}
	defer tlsConn.Close()
	_ = tlsConn.SetDeadline(time.Now().Add(5 * time.Second))

	if _, err := io.WriteString(tlsConn, "GET /secret HTTP/1.1\r\nHost: example.com\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatalf("write https request: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(tlsConn), nil)
	if err != nil {
		t.Fatalf("read https response: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read https body: %v", err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "https-mitm-ok" {
		t.Fatalf("got status=%d body=%q, want 200 \"https-mitm-ok\"", resp.StatusCode, string(body))
	}
}

func TestTransparentHTTPSNonTLSDoesNotPanic(t *testing.T) {
	proxy := newTransparentProxy(false)

	t.Run("immediate EOF", func(t *testing.T) {
		serverConn, clientConn := net.Pipe()
		_ = clientConn.Close()

		done := make(chan struct{})
		go func() {
			defer close(done)
			handleTLSConn(serverConn, proxy)
		}()

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("handleTLSConn did not return on EOF")
		}
	})

	t.Run("non-TLS payload closes conn", func(t *testing.T) {
		serverConn, clientConn := net.Pipe()
		defer clientConn.Close()

		done := make(chan struct{})
		go func() {
			defer close(done)
			handleTLSConn(serverConn, proxy)
		}()

		_ = clientConn.SetDeadline(time.Now().Add(2 * time.Second))
		_, _ = io.WriteString(clientConn, "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n")

		buf := make([]byte, 1)
		_, err := clientConn.Read(buf)
		if err == nil {
			t.Fatal("expected connection to be closed on non-TLS input")
		}
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			t.Fatalf("expected immediate close, got timeout: %v", err)
		}

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("handleTLSConn did not return on non-TLS input")
		}
	})
}

func TestTransparentHTTPSNonSNIClosesConn(t *testing.T) {
	proxy := newTransparentProxy(false)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := ln.Accept()
		if err != nil {
			return
		}
		handleTLSConn(c, proxy)
	}()

	rawConn, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer rawConn.Close()
	_ = rawConn.SetDeadline(time.Now().Add(2 * time.Second))

	// Using an IP address without ServerName causes crypto/tls to omit the SNI extension.
	tlsClient := tls.Client(rawConn, &tls.Config{InsecureSkipVerify: true})
	err = tlsClient.Handshake()
	if err == nil {
		t.Fatal("expected TLS handshake without SNI to fail")
	}
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		t.Fatalf("expected immediate close for non-SNI client, got timeout: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleTLSConn did not return for non-SNI client")
	}
}
