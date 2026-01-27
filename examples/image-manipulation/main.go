package main

import (
	"github.com/yx-zero/goproxy-transparent"
	"github.com/yx-zero/goproxy-transparent/ext/image"
	"image"
	"log"
	"net/http"
)

func main() {
	proxy := goproxy.NewProxyHttpServer()
	proxy.OnResponse().Do(goproxy_image.HandleImage(func(img image.Image, ctx *goproxy.ProxyCtx) image.Image {
		dx, dy := img.Bounds().Dx(), img.Bounds().Dy()

		newImg := image.NewRGBA(img.Bounds())
		for i := 0; i < dx; i++ {
			for j := 0; j <= dy; j++ {
				newImg.Set(i, j, img.At(i, dy-j-1))
			}
		}
		return newImg
	}))
	proxy.Verbose = true
	log.Fatal(http.ListenAndServe(":8080", proxy))
}
