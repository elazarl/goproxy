package goproxy_image_test

import (
	"bytes"
	"crypto/tls"
	"github.com/elazarl/goproxy"
	goproxy_image "github.com/elazarl/goproxy/ext/image"
	"image"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
)

var acceptAllCerts = &tls.Config{InsecureSkipVerify: true}

func oneShotProxy(proxy *goproxy.ProxyHttpServer, t *testing.T) (client *http.Client, s *httptest.Server) {
	s = httptest.NewServer(proxy)

	proxyUrl, _ := url.Parse(s.URL)
	tr := &http.Transport{TLSClientConfig: acceptAllCerts, Proxy: http.ProxyURL(proxyUrl)}
	client = &http.Client{Transport: tr}
	return
}

func getImage(file string, t *testing.T) image.Image {
	newimage, err := os.ReadFile(file)
	if err != nil {
		t.Fatal("Cannot read file", file, err)
	}
	img, _, err := image.Decode(bytes.NewReader(newimage))
	if err != nil {
		t.Fatal("Cannot decode image", file, err)
	}
	return img
}

func compareImage(eImg, aImg image.Image, t *testing.T) {
	if eImg.Bounds().Dx() != aImg.Bounds().Dx() || eImg.Bounds().Dy() != aImg.Bounds().Dy() {
		t.Error("image sizes different")
		return
	}
	for i := 0; i < eImg.Bounds().Dx(); i++ {
		for j := 0; j < eImg.Bounds().Dy(); j++ {
			er, eg, eb, ea := eImg.At(i, j).RGBA()
			ar, ag, ab, aa := aImg.At(i, j).RGBA()
			if er != ar || eg != ag || eb != ab || ea != aa {
				t.Error("images different at", i, j, "vals\n", er, eg, eb, ea, "\n", ar, ag, ab, aa, aa)
				return
			}
		}
	}
}

var fs = httptest.NewServer(http.FileServer(http.Dir(".")))

func localFile(url string) string { return fs.URL + "/" + url }

func TestConstantImageHandler(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	football := getImage("test_data/football.png", t)
	proxy.OnResponse().Do(goproxy_image.HandleImage(func(img image.Image, ctx *goproxy.ProxyCtx) image.Image {
		return football
	}))

	client, l := oneShotProxy(proxy, t)
	defer l.Close()

	resp, err := client.Get(localFile("test_data/panda.png"))
	if err != nil {
		t.Fatal("Cannot get panda.png", err)
	}

	img, _, err := image.Decode(resp.Body)
	if err != nil {
		t.Error("decode", err)
	} else {
		compareImage(football, img, t)
	}
}

func TestImageHandler(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()
	football := getImage("test_data/football.png", t)

	proxy.OnResponse(goproxy.UrlIs("/test_data/panda.png")).Do(goproxy_image.HandleImage(func(img image.Image, ctx *goproxy.ProxyCtx) image.Image {
		return football
	}))

	client, l := oneShotProxy(proxy, t)
	defer l.Close()

	resp, err := client.Get(localFile("test_data/panda.png"))
	if err != nil {
		t.Fatal("Cannot get panda.png", err)
	}

	img, _, err := image.Decode(resp.Body)
	if err != nil {
		t.Error("decode", err)
	} else {
		compareImage(football, img, t)
	}

	// and again
	resp, err = client.Get(localFile("test_data/panda.png"))
	if err != nil {
		t.Fatal("Cannot get panda.png", err)
	}

	img, _, err = image.Decode(resp.Body)
	if err != nil {
		t.Error("decode", err)
	} else {
		compareImage(football, img, t)
	}
}

func fatalOnErr(err error, msg string, t *testing.T) {
	if err != nil {
		t.Fatal(msg, err)
	}
}

func get(url string, client *http.Client) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	txt, err := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if err != nil {
		return nil, err
	}
	return txt, nil
}

func getOrFail(url string, client *http.Client, t *testing.T) []byte {
	txt, err := get(url, client)
	if err != nil {
		t.Fatal("Can't fetch url", url, err)
	}
	return txt
}

func TestReplaceImage(t *testing.T) {
	proxy := goproxy.NewProxyHttpServer()

	panda := getImage("test_data/panda.png", t)
	football := getImage("test_data/football.png", t)

	proxy.OnResponse(goproxy.UrlIs("/test_data/panda.png")).Do(goproxy_image.HandleImage(func(img image.Image, ctx *goproxy.ProxyCtx) image.Image {
		return football
	}))
	proxy.OnResponse(goproxy.UrlIs("/test_data/football.png")).Do(goproxy_image.HandleImage(func(img image.Image, ctx *goproxy.ProxyCtx) image.Image {
		return panda
	}))

	client, l := oneShotProxy(proxy, t)
	defer l.Close()

	imgByPandaReq, _, err := image.Decode(bytes.NewReader(getOrFail(localFile("test_data/panda.png"), client, t)))
	fatalOnErr(err, "decode panda", t)
	compareImage(football, imgByPandaReq, t)

	imgByFootballReq, _, err := image.Decode(bytes.NewReader(getOrFail(localFile("test_data/football.png"), client, t)))
	fatalOnErr(err, "decode football", t)
	compareImage(panda, imgByFootballReq, t)
}
