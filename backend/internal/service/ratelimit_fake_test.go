package service

import (
	"context"
	"sync"
	"time"
)

// fakeLimiter is a minimal in-memory ratelimit.Limiter for tests that
// need AuthService's login-throttle dependency satisfied without a real
// Redis connection. Its counting logic intentionally mirrors
// ratelimit.RedisLimiter's fixed-window semantics closely enough to
// exercise AuthService's own throttling/reset behavior in unit tests,
// while tests unconcerned with throttling can simply configure a limit
// high enough never to trip.
type fakeLimiter struct {
	mu     sync.Mutex
	counts map[string]int
}

func newFakeLimiter() *fakeLimiter {
	return &fakeLimiter{counts: make(map[string]int)}
}

func (f *fakeLimiter) Allow(_ context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts[key]++
	if f.counts[key] > limit {
		return false, window, nil
	}
	return true, 0, nil
}

func (f *fakeLimiter) Reset(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.counts, key)
	return nil
}
