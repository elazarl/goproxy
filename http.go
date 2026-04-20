package goproxy

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

func (proxy *ProxyHttpServer) handleHttp(w http.ResponseWriter, r *http.Request) {
	ctx := &ProxyCtx{Req: r, Session: atomic.AddInt64(&proxy.sess, 1), Proxy: proxy}

	ctx.Logf("Got request %v %v %v %v", r.URL.Path, r.Host, r.Method, r.URL.String())
	if !r.URL.IsAbs() {
		proxy.NonproxyHandler.ServeHTTP(w, r)
		return
	}
	r, resp := proxy.filterRequest(r, ctx)

	if resp == nil {
		if !proxy.KeepHeader {
			RemoveProxyHeaders(ctx, r)
		}

		var err error
		resp, err = ctx.RoundTrip(r)
		if err != nil {
			ctx.Error = err
		}
	}

	var origBody io.ReadCloser

	if resp != nil {
		origBody = resp.Body
		defer origBody.Close()
	}

	resp = proxy.filterResponse(resp, ctx)

	if resp == nil {
		var errorString string
		if ctx.Error != nil {
			errorString = "error read response " + r.URL.Host + " : " + ctx.Error.Error()
			ctx.Logf(errorString)
			http.Error(w, ctx.Error.Error(), http.StatusInternalServerError)
		} else {
			errorString = "error read response " + r.URL.Host
			ctx.Logf(errorString)
			http.Error(w, errorString, http.StatusInternalServerError)
		}
		return
	}
	ctx.Logf("Copying response to client %v [%d]", resp.Status, resp.StatusCode)
	// http.ResponseWriter will take care of filling the correct response length
	// Setting it now, might impose wrong value, contradicting the actual new
	// body the user returned.
	// We keep the original body to remove the header only if things changed.
	// This will prevent problems with HEAD requests where there's no body, yet,
	// the Content-Length header should be set.
	if origBody != resp.Body {
		resp.Header.Del("Content-Length")
	}
	copyHeaders(w.Header(), resp.Header, proxy.KeepDestinationHeaders)

	// Announce trailers known at this point (HTTP/1.1 with pre-announced
	// Trailer header). Setting "Trailer" before WriteHeader makes
	// http.Server commit to chunked encoding (h1) or a trailing HEADERS
	// frame (h2), which is required for any trailers to be forwarded.
	// Mirrors net/http/httputil.ReverseProxy.
	announcedTrailers := len(resp.Trailer)
	if announcedTrailers > 0 {
		trailerKeys := make([]string, 0, announcedTrailers)
		for k := range resp.Trailer {
			trailerKeys = append(trailerKeys, k)
		}
		w.Header().Add("Trailer", strings.Join(trailerKeys, ", "))
	}
	w.WriteHeader(resp.StatusCode)

	if isWebSocketHandshake(resp.Header) {
		ctx.Logf("Response looks like websocket upgrade.")

		// We have already written the "101 Switching Protocols" response,
		// now we hijack the connection to send WebSocket data
		if clientConn, err := proxy.hijackConnection(ctx, w); err == nil {
			wsConn, ok := resp.Body.(io.ReadWriter)
			if !ok {
				ctx.Warnf("Unable to use Websocket connection")
				return
			}
			proxy.proxyWebsocket(ctx, wsConn, clientConn)
		}
		return
	}

	var copyWriter io.Writer = w
	// Content-Type header may also contain charset definition, so here we need to check the prefix.
	// Transfer-Encoding can be a list of comma separated values, so we use Contains() for it.
	if strings.HasPrefix(w.Header().Get("content-type"), "text/event-stream") ||
		strings.Contains(w.Header().Get("transfer-encoding"), "chunked") {
		// server-side events, flush the buffered data to the client.
		copyWriter = &flushWriter{w: w}
	}

	nr, err := io.Copy(copyWriter, resp.Body)
	if err := resp.Body.Close(); err != nil {
		ctx.Warnf("Can't close response body %v", err)
	}

	// Forward upstream response trailers. Two cases:
	//   1. resp.Trailer count == announcedTrailers: every trailer was
	//      pre-announced, so http.Server is already looking for them
	//      under the unprefixed names — write values there.
	//   2. resp.Trailer count > announcedTrailers (HTTP/2 servers, or
	//      late additions): use http.TrailerPrefix so http.Server emits
	//      them as trailers without needing the leading announcement.
	//      We still need a Flush below to force chunked encoding for
	//      bodies short enough that http.Server would otherwise inline
	//      them with Content-Length and silently drop trailers.
	if len(resp.Trailer) > 0 {
		// Force chunking even when the body is small / fully buffered.
		if rc := http.NewResponseController(w); rc != nil {
			_ = rc.Flush()
		}
	}
	if len(resp.Trailer) == announcedTrailers {
		copyHeaders(w.Header(), resp.Trailer, proxy.KeepDestinationHeaders)
	} else {
		for k, vs := range resp.Trailer {
			k = http.TrailerPrefix + k
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
	}
	ctx.Logf("Copied %v bytes to client error=%v", nr, err)
}
