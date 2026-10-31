package limitation_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/elazarl/goproxy"
	"github.com/elazarl/goproxy/ext/limitation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// maximumDuration is how long a handler may take before we consider it blocked.
const maximumDuration = 100 * time.Millisecond

// newRequest returns a request whose context is cancelled when the test ends,
// so the limiter releases its slot and no goroutine is left behind.
func newRequest(t *testing.T) *http.Request {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return (&http.Request{Host: "test.com"}).WithContext(ctx)
}

// handleAsync runs limiter.Handle in a goroutine and returns a channel that is
// closed when it returns.
func handleAsync(limiter goproxy.ReqHandler, req *http.Request) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		limiter.Handle(req, &goproxy.ProxyCtx{})
		close(done)
	}()
	return done
}

func requireDone(t *testing.T, done <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(maximumDuration):
		require.Fail(t, msg)
	}
}

func assertBlocked(t *testing.T, done <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-done:
		assert.Fail(t, msg)
	case <-time.After(maximumDuration):
	}
}

func TestConcurrentRequests(t *testing.T) {
	t.Run("empty limitation", func(t *testing.T) {
		limiter := limitation.ConcurrentRequests(0)
		requireDone(t, handleAsync(limiter, newRequest(t)), "limiter took too long")
	})

	t.Run("normal limitation", func(t *testing.T) {
		limiter := limitation.ConcurrentRequests(1)
		requireDone(t, handleAsync(limiter, newRequest(t)), "limiter took too long")
	})

	t.Run("more than the limit", func(t *testing.T) {
		limiter := limitation.ConcurrentRequests(1)
		first := newRequest(t)
		requireDone(t, handleAsync(limiter, first), "first request took too long")

		second := handleAsync(limiter, newRequest(t))
		assertBlocked(t, second, "limiter was too fast, the second request should block")
	})

	t.Run("more than the limit but one request finishes", func(t *testing.T) {
		limiter := limitation.ConcurrentRequests(1)
		firstCtx, cancelFirst := context.WithCancel(context.Background())
		first := (&http.Request{Host: "test.com"}).WithContext(firstCtx)
		requireDone(t, handleAsync(limiter, first), "first request took too long")

		second := handleAsync(limiter, newRequest(t))
		assertBlocked(t, second, "second request should block while the first holds the slot")

		cancelFirst() // releases the slot taken by the first request
		requireDone(t, second, "second request should proceed after the first finishes")
	})
}
