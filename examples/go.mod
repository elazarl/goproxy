module github.com/yx-zero/goproxy-transparent/examples/goproxy-transparent

go 1.23

require (
	github.com/coder/websocket v1.8.14
	github.com/yx-zero/goproxy-transparent v1.5.0
	github.com/yx-zero/goproxy-transparent/ext v0.0.0-20250117123040-e9229c451ab8
	github.com/inconshreveable/go-vhost v1.0.0
)

require (
	golang.org/x/net v0.35.0 // indirect
	golang.org/x/text v0.22.0 // indirect
)

replace github.com/yx-zero/goproxy-transparent => ../
