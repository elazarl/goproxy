package goproxy

import (
	"errors"
	"io"
	"net/http"
	"strings"
)

func (proxy *ProxyHttpServer) handleHttp(w http.ResponseWriter, r *http.Request) {
	ctx := &ProxyCtx{Req: r, Session: proxy.sess.Add(1), Proxy: proxy}

	ctx.Logf("Got request %v %v %v %v", r.URL.Path, r.Host, r.Method, r.URL.String())
	if !r.URL.IsAbs() {
		proxy.NonProxyHandler.ServeHTTP(w, r)
		return
	}
	r, resp := proxy.filterRequest(r, ctx)

	var responseError error
	if resp == nil {
		if !proxy.KeepProxyHeaders {
			RemoveProxyHeaders(ctx, r)
		}

		resp, responseError = ctx.RoundTrip(r)
	}

	var origBody io.ReadCloser

	if resp != nil {
		origBody = resp.Body
		defer origBody.Close()
	}

	resp = proxy.filterResponse(resp, ctx)

	if resp == nil {
		if responseError == nil {
			responseError = errors.New("error read response " + r.URL.Host)
		}
		ctx.Logf(responseError.Error())

		if ctx.Proxy.ConnectionErrHandler != nil {
			resp := ctx.Proxy.ConnectionErrHandler(ctx, responseError)
			if err := resp.Write(w); err != nil {
				ctx.Warnf("Error responding to client: %s", err)
			}
		} else {
			http.Error(w, responseError.Error(), http.StatusInternalServerError)
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
	ctx.Logf("Copied %v bytes to client error=%v", nr, err)
}
