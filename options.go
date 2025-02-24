package goproxy

import (
	"context"
	"net"
	"net/http"
)

// Note: If you add a new option X make sure you also add a WithX method on Options.

// Options are params for creating DB object.
// The logic behind them is inspired by Badger DB (hypermodeinc/badger).
//
// This package provides DefaultOptions which contains options that should
// work for most applications. Consider using that as a starting point before
// customizing it for your own needs.
//
// Each option X is documented on the WithX method.
type Options struct {
	Logger          Logger
	NonProxyHandler http.Handler
	Transport       *http.Transport
	// ErrorHandler will be invoked to return a custom response
	// to clients, when an error occurs inside goproxy request handling
	// (e.g. failure to connect to the remote server).
	ErrorHandler func(ctx *ProxyCtx, err error) *http.Response
	// ConnectDial will be used to create TCP connections for CONNECT requests
	// If it's not specified, we rely on ctx.Dialer or Transport.DialContext.
	ConnectDial func(ctx context.Context, network string, addr string) (net.Conn, error)
	CertStore   CertStorage
	// TODO: Remove AllowHTTP2 and always allow it, when we have a proper logic to parse HTTP2 requests, always allowing it
	AllowHTTP2 bool
	// When PreventCanonicalization is true, the header names present in
	// the request sent through the proxy are directly passed to the destination server,
	// instead of following the HTTP RFC for their canonicalization.
	// This is useful when the header name isn't treated as a case-insensitive
	// value by the target server, because they don't follow the specs.
	PreventCanonicalization bool
	// KeepAcceptEncoding, if true, prevents the proxy from dropping
	// Accept-Encoding headers from the client.
	//
	// Note that the outbound http.Transport may still choose to add
	// Accept-Encoding: gzip if the client did not explicitly send an
	// Accept-Encoding header. To disable this behavior, set
	// Transport.DisableCompression to true.
	KeepAcceptEncoding bool
	// KeepProxyHeaders indicates when the proxy should forward also the proxy specific headers (e.g. Proxy-Authorization)
	// to the destination server. Usually, this should be false.
	KeepProxyHeaders bool
	// KeepDestinationHeaders indicates when the proxy should retain any headers present in the http.Response
	// before proxying
	KeepDestinationHeaders bool
}

// DefaultOptions returns the recommended initial options for the proxy server.
// You can freely edit them before passing it to the proxy server initialization.
func DefaultOptions() Options {
	return Options{
		Logger: NewDefaultLogger(INFO),
		NonProxyHandler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			http.Error(w, "This is a proxy server. Does not respond to non-proxy requests.", http.StatusInternalServerError)
		}),
		Transport:   &http.Transport{TLSClientConfig: tlsClientSkipVerify, Proxy: http.ProxyFromEnvironment},
		ConnectDial: dialerFromEnv(&proxy),
	}
}
