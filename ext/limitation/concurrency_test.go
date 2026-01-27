package limitation_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/yx-zero/goproxy-transparent"
	"github.com/yx-zero/goproxy-transparent/ext/limitation"
)

func TestConcurrentRequests(t *testing.T) {
	mockRequest := &http.Request{Host: "test.com"}
	ctx := &goproxy.ProxyCtx{}
	maximumDuration := 100 * time.Millisecond

	t.Run("empty limitation", func(t *testing.T) {
		timer := time.NewTimer(maximumDuration)
		defer timer.Stop()
		done := make(chan struct{})

		go func() {
			zeroLimiter := limitation.ConcurrentRequests(0)
			zeroLimiter.Handle(mockRequest, ctx)
			done <- struct{}{}
		}()

		select {
		case <-timer.C:
			t.Error("Limiter took too long")
		case <-done:
		}
	})

	t.Run("normal limitation", func(t *testing.T) {
		timer := time.NewTimer(maximumDuration)
		defer timer.Stop()
		done := make(chan struct{})

		go func() {
			oneLimiter := limitation.ConcurrentRequests(1)
			oneLimiter.Handle(mockRequest, ctx)
			done <- struct{}{}
		}()

		select {
		case <-timer.C:
			t.Error("Limiter took too long")
		case <-done:
		}
	})

	t.Run("more than the limit", func(t *testing.T) {
		timer := time.NewTimer(maximumDuration)
		defer timer.Stop()
		done := make(chan struct{})

		go func() {
			oneLimiter := limitation.ConcurrentRequests(1)
			oneLimiter.Handle(mockRequest, ctx)
			oneLimiter.Handle(mockRequest, ctx)
			done <- struct{}{}
		}()

		select {
		case <-timer.C:
			// Do nothing, we expect to reach the timeout
		case <-done:
			t.Error("Limiter was too fast")
		}
	})

	t.Run("more than the limit but one request finishes", func(t *testing.T) {
		timer := time.NewTimer(maximumDuration)
		defer timer.Stop()
		done := make(chan struct{})

		timeoutCtx, cancel := context.WithCancel(mockRequest.Context())
		mockRequestWithCancel := mockRequest.WithContext(timeoutCtx)

		go func() {
			oneLimiter := limitation.ConcurrentRequests(1)
			oneLimiter.Handle(mockRequestWithCancel, ctx)
			cancel()
			oneLimiter.Handle(mockRequest, ctx)
			done <- struct{}{}
		}()

		select {
		case <-timer.C:
			t.Error("Limiter took too long")
		case <-done:
		}
	})
}
