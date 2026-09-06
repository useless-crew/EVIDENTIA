package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewVerifyAuditChainTask(t *testing.T) {
	id := uuid.New()
	task, err := NewVerifyAuditChainTask(id)
	require.NoError(t, err)
	assert.Equal(t, TypeVerifyAuditChain, task.Type())

	var payload VerifyAuditChainPayload
	require.NoError(t, json.Unmarshal(task.Payload(), &payload))
	assert.Equal(t, id, payload.VerificationID, "the task payload must carry ONLY the verification id — no client-controllable hash/canonicalization/result field")
}

type fakeVerifier struct {
	calledWith uuid.UUID
	err        error
}

func (f *fakeVerifier) RunVerification(_ context.Context, verificationID uuid.UUID) error {
	f.calledWith = verificationID
	return f.err
}

func TestAuditVerificationHandler_ProcessTask_Success(t *testing.T) {
	verifier := &fakeVerifier{}
	handler := NewAuditVerificationHandler(verifier)

	id := uuid.New()
	task, err := NewVerifyAuditChainTask(id)
	require.NoError(t, err)

	require.NoError(t, handler.ProcessTask(context.Background(), task))
	assert.Equal(t, id, verifier.calledWith)
}

func TestAuditVerificationHandler_ProcessTask_PropagatesOperationalError(t *testing.T) {
	wantErr := errors.New("database unavailable")
	verifier := &fakeVerifier{err: wantErr}
	handler := NewAuditVerificationHandler(verifier)

	task, err := NewVerifyAuditChainTask(uuid.New())
	require.NoError(t, err)

	err = handler.ProcessTask(context.Background(), task)
	require.Error(t, err, "an operational failure must be returned so Asynq retries — never swallowed")
	assert.ErrorIs(t, err, wantErr)
}

func TestAuditVerificationHandler_ProcessTask_MalformedPayloadSkipsRetry(t *testing.T) {
	verifier := &fakeVerifier{}
	handler := NewAuditVerificationHandler(verifier)

	task := asynq.NewTask(TypeVerifyAuditChain, []byte("not json"))
	err := handler.ProcessTask(context.Background(), task)
	require.Error(t, err)
	assert.ErrorIs(t, err, asynq.SkipRetry, "a malformed payload can never succeed on retry")
}

func TestIsRetriesExhausted(t *testing.T) {
	t.Run("more attempts remain", func(t *testing.T) {
		assert.False(t, isRetriesExhausted(1, 3, errors.New("transient")))
	})
	t.Run("last attempt also failed", func(t *testing.T) {
		assert.True(t, isRetriesExhausted(3, 3, errors.New("transient")))
	})
	t.Run("retried beyond max (defensive)", func(t *testing.T) {
		assert.True(t, isRetriesExhausted(4, 3, errors.New("transient")))
	})
	t.Run("SkipRetry is always immediately exhausted", func(t *testing.T) {
		assert.True(t, isRetriesExhausted(0, 3, asynq.SkipRetry))
	})
}

type fakeFailureRecorder struct {
	markedID  uuid.UUID
	marked    bool
	returnErr error
}

func (f *fakeFailureRecorder) MarkVerificationOperationallyFailed(_ context.Context, verificationID uuid.UUID, _ error) error {
	f.markedID = verificationID
	f.marked = true
	return f.returnErr
}

func TestNewAuditVerificationErrorHandler_IgnoresOtherTaskTypes(t *testing.T) {
	recorder := &fakeFailureRecorder{}
	handler := NewAuditVerificationErrorHandler(recorder, discardLogger())

	otherTask := asynq.NewTask("some:other-task", []byte(`{}`))
	handler.HandleError(context.Background(), otherTask, errors.New("boom"))

	assert.False(t, recorder.marked, "an error handler for a DIFFERENT task type must never mark an audit verification failed")
}

func TestNewAuditVerificationErrorHandler_MalformedPayloadStillGetsLogged(t *testing.T) {
	recorder := &fakeFailureRecorder{}
	handler := NewAuditVerificationErrorHandler(recorder, discardLogger())

	task := asynq.NewTask(TypeVerifyAuditChain, []byte("not json"))
	// Should not panic despite being unable to extract a verification ID.
	handler.HandleError(context.Background(), task, errors.New("boom"))
	assert.False(t, recorder.marked)
}
