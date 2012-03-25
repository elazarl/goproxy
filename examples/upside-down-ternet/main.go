package main

import (
	"github.com/elazarl/goproxy"
	"github.com/elazarl/goproxy/ext/image"
	"syscall"
	"os"
	"os/signal"
	"runtime/pprof"
	"flag"
	"image"
	"log"
	"net"
	"net/http"
	"reflect"
)

var cpuprofile = flag.String("cpuprofile", "", "write cpu profile to file")

func callSlice(ins ...interface{}) []reflect.Value {
	out := make([]reflect.Value, len(ins))
	for i, in := range ins {
		out[i] = reflect.ValueOf(in)
	}
	return out
}
func upside(img image.Image, _ *http.Response, _ *http.Request) image.Image {
	dx, dy := img.Bounds().Dx(), img.Bounds().Dy()

	nimg := image.NewRGBA(img.Bounds())
	for i := 0; i < dx; i++ {
		for j := 0; j <= dy; j++ {
			nimg.Set(i, j, img.At(i, dy-j-1))
		}
	}
	return nimg
}

func main() {
	flag.Parse()
	if *cpuprofile != "" {
		f,err := os.Create(*cpuprofile)
		if err != nil {log.Fatal(err)}
		println("Starting CPU profile",*cpuprofile)
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatal(err)
		}
	}
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
	l,err := net.Listen("tcp",":8080")
	if err != nil {log.Fatal(err)}
	sig := make(chan os.Signal)
	signal.Notify(sig,syscall.SIGINT)

	go func(c chan os.Signal) {
		<-c
		println("SIGINT")
		if *cpuprofile != "" {
			pprof.StopCPUProfile()
			println("Stopped CPU profile")
		}
		if err := l.Close(); err != nil {
			println("Cannot close server",err.Error())
			os.Exit(-1)
		}
	}(sig)
	log.Fatal(http.Serve(l, proxy))
}
