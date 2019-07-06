package goproxy

import (
	"io"
	"sync"

	"github.com/gorilla/websocket"
)

func wsRelay(ctx *ProxyCtx, src, dst *websocket.Conn, wg *sync.WaitGroup) {
	// To avoid allocation of temp buf in io.Copy()
	buf := make([]byte, 4*1024)

	ctx.Logf("starting WS-relay, src: %p, dst: %p", src, dst)
	for {
		t, in, err := src.NextReader()

		if err != nil {
			break
		}

		out, err := dst.NextWriter(t)

		if err != nil {
			break
		}

		if _, err := io.CopyBuffer(out, in, buf); err != nil {
			break
		}

		if err := out.Close(); err != nil {
			break
		}
	}
	ctx.Logf("done with WS-relay, src: %p, dst: %p", src, dst)

	// We close both ends so that the other goroutine of this function
	// serving the other direction can terminate.
	src.Close()
	dst.Close()

	wg.Done()
}
