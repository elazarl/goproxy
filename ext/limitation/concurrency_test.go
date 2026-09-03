package limitation_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/elazarl/goproxy"
	"github.com/elazarl/goproxy/ext/limitation"
	"github.com/stretchr/testify/assert"
)

// maximumDuration is how long a handler may take before we consider it blocked.
const maximumDuration = 100 * time.Millisecond

// completesWithin reports whether handle returns before maximumDuration elapses.
func completesWithin(handle func()) bool {
	done := make(chan struct{})
	go func() {
		handle()
		close(done)
	}()

	timer := time.NewTimer(maximumDuration)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func TestConcurrentRequests(t *testing.T) {
	req := &http.Request{Host: "test.com"}
	proxyCtx := &goproxy.ProxyCtx{}

	t.Run("empty limitation", func(t *testing.T) {
		limiter := limitation.ConcurrentRequests(0)

		assert.True(t, completesWithin(func() {
			limiter.Handle(req, proxyCtx)
		}), "limiter took too long")
	})

	t.Run("normal limitation", func(t *testing.T) {
		limiter := limitation.ConcurrentRequests(1)

		assert.True(t, completesWithin(func() {
			limiter.Handle(req, proxyCtx)
		}), "limiter took too long")
	})

	t.Run("more than the limit", func(t *testing.T) {
		limiter := limitation.ConcurrentRequests(1)

		assert.False(t, completesWithin(func() {
			limiter.Handle(req, proxyCtx)
			limiter.Handle(req, proxyCtx)
		}), "limiter was too fast, the second request should block")
	})

	t.Run("more than the limit but one request finishes", func(t *testing.T) {
		limiter := limitation.ConcurrentRequests(1)
		cancelCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cancellableReq := req.WithContext(cancelCtx)

		assert.True(t, completesWithin(func() {
			limiter.Handle(cancellableReq, proxyCtx)
			cancel() // releases the slot taken by the first request
			limiter.Handle(req, proxyCtx)
		}), "limiter took too long")
	})
}
