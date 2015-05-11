package main

import (
	"image"
	"log"

	"github.com/abourget/goproxy"
	"github.com/abourget/goproxy/ext/image"
)

func main() {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnResponse().Do(goproxy_image.HandleImage(func(img image.Image, ctx *goproxy.ProxyCtx) image.Image {
		dx, dy := img.Bounds().Dx(), img.Bounds().Dy()

		nimg := image.NewRGBA(img.Bounds())
		for i := 0; i < dx; i++ {
			for j := 0; j <= dy; j++ {
				nimg.Set(i, j, img.At(i, dy-j-1))
			}
		}
		return nimg
	}))
	proxy.Verbose = true
	log.Fatal(proxy.ListenAndServe(":8080"))
}
