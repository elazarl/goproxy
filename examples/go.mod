module github.com/elazarl/goproxy/examples

go 1.24.0

require (
	github.com/coder/websocket v1.8.14
	github.com/elazarl/goproxy v1.8.4
	github.com/elazarl/goproxy/ext v0.0.0-20260131165438-44388f68745c
	github.com/inconshreveable/go-vhost v1.0.0
)

require (
	golang.org/x/net v0.50.0 // indirect
	golang.org/x/text v0.34.0 // indirect
)

replace github.com/elazarl/goproxy => ../
