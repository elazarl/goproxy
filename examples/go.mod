module github.com/elazarl/goproxy/examples/goproxy-transparent

require (
	github.com/Windscribe/goproxy v0.0.0-20191106214216-b139bf89b3b9 // indirect
	github.com/elazarl/goproxy v0.0.0-20191011121108-aa519ddbe484
	github.com/inconshreveable/go-vhost v0.0.0-20160627193104-06d84117953b
)

replace github.com/elazarl/goproxy => ../

go 1.13
