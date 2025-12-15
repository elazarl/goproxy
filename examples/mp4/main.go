package main

import (
	"log"
	"net/http"

	"github.com/elazarl/goproxy"
	"github.com/andreparames/goproxy/ext/mp4" // Assuming you put mp4.go here
)

// FFmpegMp4Transcode code is assumed to be in the mp4 package

func main() {
	proxy := goproxy.NewProxyHttpServer()

	proxy.OnResponse(mp4.IsMp4Video).DoFunc(
		mp4.HandleMp4Stream(
			mp4.FFmpegMp4Transcode(
				"ffmpeg",
				"-i", "-",
				"-vcodec", "libx264",
				"-preset", "ultrafast",
				"-vf", "scale=-2:720",
				"-acodec", "copy",
				"-f", "mp4",
				"-movflags", "frag_keyframe+empty_moov",
				"pipe:1",
			),
		),
	)

	// Enable verbose logging
	proxy.Verbose = true

	log.Println("Starting proxy server on :8080")
	log.Fatal(http.ListenAndServe(":8080", proxy))
}
