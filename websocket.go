package goproxy

import (
	"github.com/gorilla/websocket"
	"io"
	"sync"
)

func wsRelay(ctx *ProxyCtx, src, dst *websocket.Conn, wg *sync.WaitGroup) {
	// TODO add detection of graceful shutdown (via t == websocket.CloseMessage)

	// To avoid allocation of temp buf in io.Copy()
	buf := make([]byte, 4 * 1024)

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

	wg.Done()
}
