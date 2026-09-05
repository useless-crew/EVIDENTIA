//go:build integration

// Run with: go test -tags=integration ./internal/jobs/...
// Requires a reachable Redis (REDIS_ADDR, default localhost:6379) — the
// same docker-compose service every other integration test in this
// repository already depends on. Unlike audit_verification_test.go (pure
// unit tests, no network), this file exercises the real Asynq
// client/queue/inspector round trip: what audit_verification_test.go
// cannot verify (asynq.Task carries no public accessor for the
// queue/task-ID options it was constructed with — those are consumed
// only by the real client/broker) requires actually enqueueing against
// real Redis and reading the result back via asynq.Inspector.
package jobs

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

func redisOptFromEnv() asynq.RedisClientOpt {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	return asynq.RedisClientOpt{Addr: addr, Password: os.Getenv("REDIS_PASSWORD")}
}

// TestEnqueueVerifyAuditChain_UsesCriticalQueueAndDeterministicJobID
// proves, against real Redis, that System 12's queue-priority and
// job-traceability design actually holds for System 11's one task type:
// the enqueued task lands on QueueCritical (never the default queue),
// and its Asynq task ID is exactly AuditVerifyChainJobID(verificationID)
// — the same string internal/service.AuditService returns to the API
// caller as job_id, so a client reading job_id can find this exact task
// via `asynq.Inspector.GetTaskInfo(QueueCritical, job_id)` if ever needed
// for operational diagnosis.
func TestEnqueueVerifyAuditChain_UsesCriticalQueueAndDeterministicJobID(t *testing.T) {
	redisOpt := redisOptFromEnv()
	client := NewClient(redisOpt)
	defer client.Close()

	inspector := asynq.NewInspector(redisOpt)
	defer inspector.Close()

	verificationID := uuid.New()
	jobID := AuditVerifyChainJobID(verificationID)
	t.Cleanup(func() { _ = inspector.DeleteTask(QueueCritical, jobID) })

	require.NoError(t, client.EnqueueVerifyAuditChain(context.Background(), verificationID))

	info, err := inspector.GetTaskInfo(QueueCritical, jobID)
	require.NoError(t, err, "the task must be discoverable on QueueCritical by its deterministic job id")
	require.Equal(t, TypeVerifyAuditChain, info.Type)
	require.Equal(t, QueueCritical, info.Queue)
	require.Equal(t, jobID, info.ID)
}

// TestEnqueueVerifyAuditChain_DeterministicIDRejectsDuplicateEnqueue is
// System 12's defense-in-depth idempotency layer (see
// DeterministicTaskID's own doc comment): enqueueing the SAME
// verification id twice must never create two Asynq tasks — the second
// attempt fails with asynq.ErrTaskIDConflict, underneath (never instead
// of) audit_verifications' own database-level single-active-run
// constraint, which is what internal/service.AuditService.StartVerification
// actually relies on day to day.
func TestEnqueueVerifyAuditChain_DeterministicIDRejectsDuplicateEnqueue(t *testing.T) {
	redisOpt := redisOptFromEnv()
	client := NewClient(redisOpt)
	defer client.Close()

	inspector := asynq.NewInspector(redisOpt)
	defer inspector.Close()

	verificationID := uuid.New()
	jobID := AuditVerifyChainJobID(verificationID)
	t.Cleanup(func() { _ = inspector.DeleteTask(QueueCritical, jobID) })

	require.NoError(t, client.EnqueueVerifyAuditChain(context.Background(), verificationID))

	err := client.EnqueueVerifyAuditChain(context.Background(), verificationID)
	require.Error(t, err)
	require.True(t, errors.Is(err, asynq.ErrTaskIDConflict), "a second enqueue for the same verification id must be rejected at the Asynq layer, not silently create a duplicate task")
}
