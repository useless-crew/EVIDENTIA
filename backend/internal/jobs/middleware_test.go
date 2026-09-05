package jobs

import (
	"context"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoggingMiddleware_PassesThroughSuccess(t *testing.T) {
	var called bool
	next := asynq.HandlerFunc(func(ctx context.Context, task *asynq.Task) error {
		called = true
		return nil
	})

	wrapped := LoggingMiddleware(discardLogger())(next)
	task := asynq.NewTask("test:noop", []byte(`{}`))
	require.NoError(t, wrapped.ProcessTask(context.Background(), task))
	assert.True(t, called, "the middleware must invoke the wrapped handler")
}

func TestLoggingMiddleware_PropagatesFailure(t *testing.T) {
	wantErr := errors.New("boom")
	next := asynq.HandlerFunc(func(ctx context.Context, task *asynq.Task) error {
		return wantErr
	})

	wrapped := LoggingMiddleware(discardLogger())(next)
	task := asynq.NewTask("test:noop", []byte(`{}`))
	err := wrapped.ProcessTask(context.Background(), task)
	assert.ErrorIs(t, err, wantErr, "the middleware must never swallow or replace the wrapped handler's error")
}

func TestLoggingMiddleware_NeverPanicsWithoutAsynqServerContext(t *testing.T) {
	// asynq.GetTaskID/GetQueueName/GetRetryCount/GetMaxRetry all read from
	// context keys only asynq's own server machinery populates — a plain
	// context.Background() (exactly what a unit test, and this test itself,
	// supplies) must never cause the middleware to panic; it should just
	// log empty/zero values for those fields.
	next := asynq.HandlerFunc(func(ctx context.Context, task *asynq.Task) error { return nil })
	wrapped := LoggingMiddleware(discardLogger())(next)
	assert.NotPanics(t, func() {
		_ = wrapped.ProcessTask(context.Background(), asynq.NewTask("test:noop", []byte(`{}`)))
	})
}
