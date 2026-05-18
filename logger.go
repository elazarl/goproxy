package goproxy

// Logger is the interface used by ProxyHttpServer to emit log messages.
// Any type implementing Printf with the standard fmt.Sprintf signature satisfies this interface.
// By default, NewProxyHttpServer sets Logger to log.New(os.Stderr, "", log.LstdFlags).
// Log output is emitted only when ProxyHttpServer.Verbose is set to true.
type Logger interface {
	Printf(format string, v ...any)
}
