// Package jobs is Evidentia's ONE Asynq integration — System 12's
// reusable background-job infrastructure, introduced when System 11's
// audit-chain verification became this package's first task type and
// generalized (queue priority, error classification, structured logging)
// rather than duplicated when System 12 later needed the same
// enqueue/process/retry/observability machinery for any future task
// type. There is, and must remain, only ONE *asynq.Server/*asynq.Client
// pair in this codebase (see queue.go/client.go) — a future task type
// registers a new Handler on the same NewMux and, if it is genuinely
// security-critical, uses QueueCritical; it never stands up a second
// worker or a second queueing mechanism.
//
// Shared, task-type-agnostic pieces: Client (enqueueing — this file),
// NewServer/NewMux/the queue priority constants (queue.go),
// LoggingMiddleware (middleware.go — every task type's one observability
// point), FailureCategory/Permanent/CategoryOf (errors.go — the
// TRANSIENT/PERMANENT/SECURITY/INTEGRITY retry-classification
// vocabulary), and DeterministicTaskID (ids.go — traceable job_id +
// defense-in-depth idempotency). Task-type-SPECIFIC pieces live in their
// own file (audit_verification.go is the only one today) and depend on
// nothing in internal/service — see that file's AuditVerifier/
// AuditFailureRecorder interfaces, defined in THIS package and satisfied
// structurally by *service.AuditService, specifically so internal/
// service can depend on internal/jobs (to enqueue a task) without an
// import cycle back the other way. See docs/BACKGROUND_JOBS.md for the
// complete architecture and the reasoning behind which Systems 1-11
// operations were (and were not) moved onto this infrastructure.
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
