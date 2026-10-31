package goproxy_image_test

import (
	"bytes"
	"image"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"testing"

	"github.com/elazarl/goproxy"
	goproxy_image "github.com/elazarl/goproxy/ext/image"
	"github.com/elazarl/goproxy/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// repoRoot is the directory holding the shared test_data images. The tests run
// from ext/image, while the images live at the root of the repository.
const repoRoot = "../.."

// loadImage decodes one of the images in test_data from disk.
func loadImage(t *testing.T, name string) image.Image {
	t.Helper()
	raw, err := os.ReadFile(path.Join(repoRoot, "test_data", name))
	require.NoError(t, err, "cannot read test_data/%s", name)
	img, _, err := image.Decode(bytes.NewReader(raw))
	require.NoError(t, err, "cannot decode test_data/%s", name)
	return img
}

// newFileServer serves the repository root, so that test_data images are
// reachable under /test_data/<name>. It returns a function building the URL of
// an image served that way.
func newFileServer(t *testing.T) func(name string) string {
	t.Helper()
	s := httptest.NewServer(http.FileServer(http.Dir(repoRoot)))
	t.Cleanup(s.Close)
	return func(name string) string { return s.URL + "/test_data/" + name }
}

// assertSameImage compares two images pixel by pixel.
func assertSameImage(t *testing.T, expected, actual image.Image) {
	t.Helper()
	if !assert.Equal(t, expected.Bounds().Size(), actual.Bounds().Size(), "image size") {
		return
	}
	for x := 0; x < expected.Bounds().Dx(); x++ {
		for y := 0; y < expected.Bounds().Dy(); y++ {
			er, eg, eb, ea := expected.At(x, y).RGBA()
			ar, ag, ab, aa := actual.At(x, y).RGBA()
			expectedPixel := [4]uint32{er, eg, eb, ea}
			actualPixel := [4]uint32{ar, ag, ab, aa}
			if !assert.Equal(t, expectedPixel, actualPixel, "pixel at (%d,%d)", x, y) {
				return
			}
		}
	}
}

// decodeImage decodes an image the proxy returned in a response body.
func decodeImage(t *testing.T, body []byte) image.Image {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(body))
	require.NoError(t, err, "cannot decode proxied image")
	return img
}

func replaceWith(replacement image.Image) goproxy.RespHandler {
	return goproxy_image.HandleImage(func(_ image.Image, _ *goproxy.ProxyCtx) image.Image {
		return replacement
	})
}

func TestHandleImageReplacesEveryImage(t *testing.T) {
	football := loadImage(t, "football.png")

	proxy := goproxy.NewProxyHttpServer()
	proxy.OnResponse().Do(replaceWith(football))
	client, _ := testutil.NewProxy(t, proxy)
	fileURL := newFileServer(t)

	body := testutil.GetOrFail(t, client, fileURL("panda.png"))

	assertSameImage(t, football, decodeImage(t, body))
}

func TestHandleImageReplacesMatchingURLOnRepeatedRequests(t *testing.T) {
	football := loadImage(t, "football.png")

	proxy := goproxy.NewProxyHttpServer()
	proxy.OnResponse(goproxy.UrlIs("/test_data/panda.png")).Do(replaceWith(football))
	client, _ := testutil.NewProxy(t, proxy)
	fileURL := newFileServer(t)

	// The handler must keep working when the same image is requested again.
	for i := range 2 {
		body := testutil.GetOrFail(t, client, fileURL("panda.png"))
		require.NotEmpty(t, body, "request %d returned an empty body", i+1)
		assertSameImage(t, football, decodeImage(t, body))
	}
}

func TestHandleImageSwapsTwoImages(t *testing.T) {
	panda := loadImage(t, "panda.png")
	football := loadImage(t, "football.png")

	proxy := goproxy.NewProxyHttpServer()
	proxy.OnResponse(goproxy.UrlIs("/test_data/panda.png")).Do(replaceWith(football))
	proxy.OnResponse(goproxy.UrlIs("/test_data/football.png")).Do(replaceWith(panda))
	client, _ := testutil.NewProxy(t, proxy)
	fileURL := newFileServer(t)

	pandaResponse := testutil.GetOrFail(t, client, fileURL("panda.png"))
	assertSameImage(t, football, decodeImage(t, pandaResponse))

	footballResponse := testutil.GetOrFail(t, client, fileURL("football.png"))
	assertSameImage(t, panda, decodeImage(t, footballResponse))
}
