// Package ratelimit provides a small Redis-backed fixed-window request
// limiter. Its first (and, as of System 15, only) caller is
// internal/service.AuthService.Login, which uses it to slow down
// credential-stuffing/brute-force attempts against POST /auth/login — see
// that method for the concrete per-IP and per-account keys and
// thresholds. The type here is deliberately generic (key + limit +
// window), not login-specific, so a later caller needing the same
// primitive does not need a second implementation.
package ratelimit

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// Limiter is the behavior internal/service.AuthService depends on, so
// tests can substitute an in-memory fake without a real Redis connection
// (mirrors the internal/audit.Recorder interface pattern already used
// throughout this codebase).
type Limiter interface {
	// Allow reports whether an attempt identified by key may proceed,
	// given at most limit attempts per window. retryAfter is only
	// meaningful when allowed is false. err is non-nil only on an
	// underlying Redis failure — see RedisLimiter.Allow's doc comment for
	// why callers should treat that as fail-OPEN, not fail-closed.
	Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error)

	// Reset clears key's counter, e.g. after a successful login so a
	// legitimate user is not penalized by their own earlier failed
	// attempts for the rest of the window.
	Reset(ctx context.Context, key string) error
}

// RedisLimiter implements Limiter as a fixed-window counter: the first
// Allow call for a key within a window creates a counter with a TTL equal
// to window; every subsequent call within that window increments it. Once
// the count exceeds limit, Allow reports false with the remaining time
// until the window resets.
//
// A fixed window (rather than a sliding window or token bucket) is a
// deliberate simplicity choice: it can briefly admit up to roughly 2x
// limit attempts across a window boundary. That is an acceptable
// trade-off for a defense-in-depth control backing the bcrypt/account-
// status checks that remain the AUTHORITATIVE gate in AuthService.Login —
// this type only adds friction against automated brute-forcing, it is
// never the sole thing standing between an attacker and a valid session.
type RedisLimiter struct {
	client *redis.Client
	prefix string
}

// NewRedisLimiter builds a RedisLimiter using client — the SAME shared
// go-redis client internal/cache.Cache already validated at startup, per
// this project's one-connection-pool-per-infrastructure-dependency
// convention (see internal/app.New's doc comments on eventPublisher/
// sseManager for the same pattern).
func NewRedisLimiter(client *redis.Client) *RedisLimiter {
	return &RedisLimiter{client: client, prefix: "ratelimit:"}
}

// Allow never fails closed: a Redis outage must not be able to lock every
// user out of login. On a Redis error, Allow returns (true, 0, err) — the
// caller logs err (this codebase's existing convention for a non-fatal
// infrastructure hiccup — c.f. internal/audit.Recorder.Record, which
// never returns an error at all for the same reason) and proceeds as if
// the attempt were allowed, relying on bcrypt/account-status checks
// downstream to remain the real gate.
func (l *RedisLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error) {
	fullKey := l.prefix + key

	count, err := l.client.Incr(ctx, fullKey).Result()
	if err != nil {
		return true, 0, err
	}
	if count == 1 {
		if err := l.client.Expire(ctx, fullKey, window).Err(); err != nil {
			return true, 0, err
		}
	}
	if count <= int64(limit) {
		return true, 0, nil
	}

	ttl, ttlErr := l.client.TTL(ctx, fullKey).Result()
	if ttlErr != nil || ttl < 0 {
		ttl = window
	}
	return false, ttl, nil
}

// Reset deletes key's counter outright (rather than decrementing it), so
// a successful login gives the account a full, fresh quota immediately.
func (l *RedisLimiter) Reset(ctx context.Context, key string) error {
	return l.client.Del(ctx, l.prefix+key).Err()
}
