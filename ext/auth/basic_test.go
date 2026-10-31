package auth_test

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elazarl/goproxy"
	"github.com/elazarl/goproxy/ext/auth"
	"github.com/elazarl/goproxy/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	authRealm    = "my_realm"
	authUser     = "user"
	authPassword = "open sesame"
)

// validCredentials is the authentication callback used by the tests below.
func validCredentials(user, password string) bool {
	return user == authUser && password == authPassword
}

// curlPath returns the path of the curl binary, skipping the test if curl
// is not installed.
func curlPath(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("curl")
	if err != nil {
		t.Skipf("curl is not available: %v", err)
	}
	return path
}

func TestBasicConnectAuthWithCurl(t *testing.T) {
	const body = ":c>"
	curl := curlPath(t)

	backend := httptest.NewTLSServer(testutil.ConstantHandler(body))
	t.Cleanup(backend.Close)

	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().HandleConnect(auth.BasicConnect(authRealm, validCredentials))
	_, proxyServer := testutil.NewProxy(t, proxy)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// CombinedOutput so that a curl error message shows up in the failure.
	out, err := exec.CommandContext(ctx, curl,
		"--silent", "--show-error", "--insecure",
		"-x", proxyServer.URL,
		"-U", authUser+":"+authPassword,
		"-p",
		"--url", backend.URL+"/[1-3]",
	).CombinedOutput()
	require.NoError(t, err, "curl output: %s", out)
	assert.Equal(t, strings.Repeat(body, 3), string(out))
}

func TestBasicAuthWithCurl(t *testing.T) {
	const body = ":c>"
	curl := curlPath(t)

	backend := httptest.NewServer(testutil.ConstantHandler(body))
	t.Cleanup(backend.Close)

	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().Do(auth.Basic(authRealm, validCredentials))
	_, proxyServer := testutil.NewProxy(t, proxy)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// CombinedOutput so that a curl error message shows up in the failure.
	out, err := exec.CommandContext(ctx, curl,
		"--silent", "--show-error",
		"-x", proxyServer.URL,
		"-U", authUser+":"+authPassword,
		"--url", backend.URL+"/[1-3]",
	).CombinedOutput()
	require.NoError(t, err, "curl output: %s", out)
	assert.Equal(t, strings.Repeat(body, 3), string(out))
}

func TestBasicAuth(t *testing.T) {
	const body = "hello"
	backend := httptest.NewServer(testutil.ConstantHandler(body))
	t.Cleanup(backend.Close)

	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().Do(auth.Basic(authRealm, validCredentials))
	client, _ := testutil.NewProxy(t, proxy)

	newRequest := func() *http.Request {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, backend.URL, nil)
		require.NoError(t, err)
		return req
	}

	// Without credentials the proxy must ask for them.
	resp, err := client.Do(newRequest())
	require.NoError(t, err)
	assert.Equal(t, http.StatusProxyAuthRequired, resp.StatusCode)
	assert.Equal(t, "Basic realm="+authRealm, resp.Header.Get("Proxy-Authenticate"))
	require.NoError(t, resp.Body.Close())

	// With valid credentials the request reaches the backend.
	req := newRequest()
	req.Header.Set("Proxy-Authorization",
		"Basic "+base64.StdEncoding.EncodeToString([]byte(authUser+":"+authPassword)))
	resp, err = client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	msg, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, body, string(msg))
}

// TestWithBrowser is a manual test: an easy way to check that authentication
// works with a real browser. To run it:
//
//	$ go test -v -run TestWithBrowser -- server
//
// Configure a browser to use the printed proxy address, browse through the
// proxy, then stop the test with Ctrl-C. It fails if the proxy was never used.
func TestWithBrowser(t *testing.T) {
	if os.Args[len(os.Args)-1] != "server" {
		t.Skip("manual test, run with: go test -v -run TestWithBrowser -- server")
	}

	const browserUser, browserPassword = "user", "1234"
	var accesses atomic.Int32

	proxy := goproxy.NewProxyHttpServer()
	proxy.OnRequest().Do(auth.Basic(authRealm, func(user, password string) bool {
		accesses.Add(1)
		return user == browserUser && password == browserPassword
	}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", "localhost:0")
	require.NoError(t, err)
	t.Logf("proxy listening on %s, user %q, password %q",
		listener.Addr(), browserUser, browserPassword)

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	server := &http.Server{Handler: proxy, ReadHeaderTimeout: 10 * time.Second}
	err = server.Serve(listener)
	if !errors.Is(err, net.ErrClosed) {
		require.NoError(t, err)
	}
	assert.Positive(t, accesses.Load(), "no one accessed the proxy")
}
