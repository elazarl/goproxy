package main

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/elazarl/goproxy/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readTestFile(t *testing.T, fname string) string {
	t.Helper()
	b, err := os.ReadFile(fname)
	require.NoError(t, err, "cannot read %s", fname)
	return string(b)
}

// newJQueryProxy starts a jQuery version checking proxy and returns a client
// routed through it, plus the buffer that collects the proxy warnings.
func newJQueryProxy(t *testing.T) (*http.Client, *bytes.Buffer) {
	t.Helper()
	var warnings bytes.Buffer
	proxy := NewJQueryVersionProxy()
	proxy.Logger = log.New(&warnings, "", 0)
	client, _ := testutil.NewProxy(t, proxy)
	return client, &warnings
}

// newFileServer serves the HTML fixtures of this directory.
func newFileServer(t *testing.T) *httptest.Server {
	t.Helper()
	fs := httptest.NewServer(http.FileServer(http.Dir(".")))
	t.Cleanup(fs.Close)
	return fs
}

func TestDefectiveScriptParser(t *testing.T) {
	// A page without script tags has no script sources.
	assert.Empty(t, findScriptSrc(`<!DOCTYPE HTML>
    <html>
    <body>

    <video width="320" height="240" controls="controls">
      <source src="movie.mp4" type="video/mp4" />
	<source src="movie.ogg" type="video/ogg" />
	  <source src="movie.webm" type="video/webm" />
	  Your browser does not support the video tag.
	  </video>

	  </body>
	  </html>`))

	assert.Equal(t, []string{
		"http://partner.googleadservices.com/gampad/google_service.js",
		"//translate.google.com/translate_a/element.js?cb=googleTranslateElementInit",
	}, findScriptSrc(readTestFile(t, "w3schools.html")), "w3schools.html src scripts are not recognized")

	assert.Equal(t, []string{
		"http://ajax.googleapis.com/ajax/libs/jquery/1.4.2/jquery.min.js",
		"http://code.jquery.com/jquery-1.4.2.min.js",
		"http://static.jquery.com/files/rocker/scripts/custom.js",
		"http://static.jquery.com/donate/donate.js",
	}, findScriptSrc(readTestFile(t, "jquery_homepage.html")), "jquery_homepage.html src scripts are not recognized")
}

// TestProxyServiceTwoVersions checks that the proxy stays quiet while a host
// serves a single jQuery version, and warns once a second page of the same
// host references a different one.
func TestProxyServiceTwoVersions(t *testing.T) {
	fs := newFileServer(t)
	client, warnings := newJQueryProxy(t)

	testutil.GetOrFail(t, client, fs.URL+"/w3schools.html")
	testutil.GetOrFail(t, client, fs.URL+"/php_man.html")

	if firstWarnings := warnings.String(); firstWarnings != "" {
		assert.Contains(t, firstWarnings, " uses jquery ", "shouldn't warn on a single URL")
	}

	testutil.GetOrFail(t, client, fs.URL+"/jquery1.html")

	// php_man.html uses jQuery 1.3.2, jquery1.html uses 1.4.
	contradiction := warnings.String()
	assert.Contains(t, contradiction, "http://ajax.googleapis.com/ajax/libs/jquery/1.3.2/jquery.min.js")
	assert.Contains(t, contradiction, "jquery.1.4.js")
	assert.Contains(t, contradiction, "Contradicting")
}

// TestProxyService checks that two contradicting jQuery versions on a single
// page are reported.
func TestProxyService(t *testing.T) {
	fs := newFileServer(t)
	client, warnings := newJQueryProxy(t)

	testutil.GetOrFail(t, client, fs.URL+"/jquery_homepage.html")

	contradiction := warnings.String()
	assert.Contains(t, contradiction, "http://ajax.googleapis.com/ajax/libs/jquery/1.4.2/jquery.min.js")
	assert.Contains(t, contradiction, "http://code.jquery.com/jquery-1.4.2.min.js")
	assert.Contains(t, contradiction, "Contradicting")
}
