module github.com/elazarl/goproxy/examples/goproxy-transparent

go 1.12

replace github.com/elazarl/goproxy => ../

replace github.com/elazarl/goproxy/ext => ../ext

require (
	github.com/elazarl/goproxy v0.0.0-20190711103511-473e67f1d7d2
	github.com/elazarl/goproxy/ext v0.0.0-00010101000000-000000000000
	github.com/inconshreveable/go-vhost v0.0.0-20160627193104-06d84117953b
)
