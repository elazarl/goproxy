module github.com/elazarl/goproxy/examples/goproxy-transparent

go 1.20

require (
	github.com/elazarl/goproxy v0.0.0-20241217120900-7711dfa3811c
	github.com/elazarl/goproxy/ext v0.0.0-20241217120900-7711dfa3811c
	github.com/gorilla/websocket v1.5.3
	github.com/inconshreveable/go-vhost v1.0.0
)

require (
	github.com/rogpeppe/go-charset v0.0.0-20190617161244-0dc95cdf6f31 // indirect
	golang.org/x/net v0.32.0 // indirect
	golang.org/x/text v0.21.0 // indirect
)

replace github.com/elazarl/goproxy => ../
