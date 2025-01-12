module github.com/elazarl/goproxy/examples/goproxy-transparent

go 1.20

require (
	github.com/elazarl/goproxy v1.3.0
	github.com/elazarl/goproxy/ext v0.0.0-20250110140559-10fc34b80676
	github.com/gorilla/websocket v1.5.3
	github.com/inconshreveable/go-vhost v1.0.0
)

require (
	golang.org/x/net v0.34.0 // indirect
	golang.org/x/text v0.21.0 // indirect
)

replace github.com/elazarl/goproxy => ../
