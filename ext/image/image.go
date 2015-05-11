package goproxy_image

import (
	"bytes"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io/ioutil"

	. "github.com/abourget/goproxy"
	"github.com/abourget/goproxy/regretable"
)

var RespIsImage = RespContentTypeIs("image/gif",
	"image/jpeg",
	"image/pjpeg",
	"application/octet-stream",
	"image/png")

var imageTypes = map[string]bool{
	"image/gif":                true,
	"image/jpeg":               true,
	"image/pjpeg":              true,
	"image/png":                true,
	"application/octet-stream": true,
}

// "image/tiff" tiff support is in external package, and rarely used, so we omitted it

func HandleImage(f func(img image.Image, ctx *ProxyCtx) image.Image) Handler {
	return HandlerFunc(func(ctx *ProxyCtx) Next {
		if ctx.Resp == nil {
			return NEXT
		}

		contentType := ctx.Resp.Header.Get("Content-Type")

		if _, ok := imageTypes[contentType]; !ok {
			return NEXT
		}

		resp := ctx.Resp
		if resp.StatusCode != 200 {
			// we might get 304 - not modified response without data
			return NEXT
		}

		const kb = 1024
		regret := regretable.NewRegretableReaderCloserSize(resp.Body, 16*kb)
		resp.Body = regret
		img, imgType, err := image.Decode(resp.Body)
		if err != nil {
			regret.Regret()
			ctx.Warnf("%s: %s", ctx.Req.Method+" "+ctx.Req.URL.String()+" Image from "+ctx.Req.RequestURI+"content type"+
				contentType+"cannot be decoded returning original image", err)
			return NEXT
		}

		result := f(img, ctx)

		buf := bytes.NewBuffer([]byte{})
		switch contentType {
		// No gif image encoder in go - convert to png
		case "image/gif", "image/png":
			if err := png.Encode(buf, result); err != nil {
				ctx.Warnf("Cannot encode image, returning orig %v %v", ctx.Req.URL.String(), err)
				return NEXT
			}
			resp.Header.Set("Content-Type", "image/png")
		case "image/jpeg", "image/pjpeg":
			if err := jpeg.Encode(buf, result, nil); err != nil {
				ctx.Warnf("Cannot encode image, returning orig %v %v", ctx.Req.URL.String(), err)
				return NEXT
			}
		case "application/octet-stream":
			switch imgType {
			case "jpeg":
				if err := jpeg.Encode(buf, result, nil); err != nil {
					ctx.Warnf("Cannot encode image as jpeg, returning orig %v %v", ctx.Req.URL.String(), err)
					return NEXT
				}
			case "png", "gif":
				if err := png.Encode(buf, result); err != nil {
					ctx.Warnf("Cannot encode image as png, returning orig %v %v", ctx.Req.URL.String(), err)
					return NEXT
				}
			}
		default:
			panic("unhandlable type" + contentType)
		}

		resp.Body = ioutil.NopCloser(buf)
		return NEXT
	})
}
