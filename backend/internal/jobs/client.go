// Package jobs is System 11's Asynq integration: enqueueing background
// tasks (Client) and processing them (queue.go's server/mux,
// audit_verification.go's handler). It depends on internal/service for
// NOTHING — see audit_verification.go's AuditVerifier/AuditFailureRecorder
// interfaces, defined in THIS package and satisfied structurally by
// *service.AuditService, specifically so internal/service can depend on
// internal/jobs (to enqueue a task when starting a verification) without
// creating an import cycle back the other way.
package jobs

import (
	"context"
	"fmt"

	"github.com/hibiken/asynq"
)

// Client wraps *asynq.Client — the ONE way any service in this codebase
// enqueues a background task. Application code depends on this type, never
// asynq directly, mirroring internal/cache.Cache's identical rationale for
// go-redis.
type Client struct {
	c *asynq.Client
}

// NewClient builds a Client against the same Redis connection parameters
// internal/cache.Cache already validated at startup (see internal/app —
// asynq needs its own *redis.Client-compatible connection options, not the
// pooled go-redis client itself, since it manages its own connection pool
// internally).
func NewClient(redisOpt asynq.RedisConnOpt) *Client {
	return &Client{c: asynq.NewClient(redisOpt)}
}

// Close releases the client's Redis connections. Safe to call once during
// graceful shutdown (see internal/app.App.Close) — and safe to call on a
// nil *Client (a no-op), matching the same tolerant-partial-construction
// convention internal/app's own tests already rely on for other optional
// fields.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	return c.c.Close()
}

// enqueue submits task, wrapping any failure in a message safe to log
// operationally (never a reason to fail the HTTP request that triggered
// it beyond a generic 500 — see internal/service.AuditService.
// StartVerification's own error handling).
func (c *Client) enqueue(ctx context.Context, task *asynq.Task, opts ...asynq.Option) error {
	if _, err := c.c.EnqueueContext(ctx, task, opts...); err != nil {
		return fmt.Errorf("jobs: enqueue %s: %w", task.Type(), err)
	}
	return nil
}
