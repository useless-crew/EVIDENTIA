package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"
)

// LoggingMiddleware is System 12's ONE structured-observability point for
// every task type registered on a *asynq.ServeMux (see NewMux) — a task
// type's own Handler never needs its own ad hoc start/duration/failure
// logging (System 11's AuditVerificationHandler.ProcessTask logged its
// own failures directly before this existed; that responsibility now
// lives here instead, applied uniformly to every future task type too).
//
// Logs, per master prompt's "Job Observability": task type, task ID,
// queue, attempt number, start, completion, duration, and — on failure —
// a safe FailureCategory (never the operation's own business data:
// nothing here ever sees a task's payload contents, only its type/ID/
// queue/retry metadata, which for every task type in this package is
// itself limited to trusted server-generated identifiers, never a secret
// — see VerifyAuditChainPayload's own doc comment). Never logs a JWT,
// password, encryption key, MinIO credential, or document content — this
// middleware has no access to any of those in the first place, and the
// task types built on top of it are designed the same way (job payloads
// carry only minimal trusted IDs, per this package's own conventions).
func LoggingMiddleware(logger *slog.Logger) asynq.MiddlewareFunc {
	return func(next asynq.Handler) asynq.Handler {
		return asynq.HandlerFunc(func(ctx context.Context, task *asynq.Task) error {
			taskID, _ := asynq.GetTaskID(ctx)
			queue, _ := asynq.GetQueueName(ctx)
			retryCount, _ := asynq.GetRetryCount(ctx)
			maxRetry, _ := asynq.GetMaxRetry(ctx)
			taskType := task.Type()

			logger.InfoContext(ctx, "job started",
				slog.String("task_type", taskType),
				slog.String("task_id", taskID),
				slog.String("queue", queue),
				slog.Int("attempt", retryCount+1),
				slog.Int("max_attempts", maxRetry+1),
			)

			start := time.Now()
			err := next.ProcessTask(ctx, task)
			duration := time.Since(start)

			if err != nil {
				logger.ErrorContext(ctx, "job failed",
					slog.String("task_type", taskType),
					slog.String("task_id", taskID),
					slog.String("queue", queue),
					slog.Int("attempt", retryCount+1),
					slog.Duration("duration", duration),
					slog.String("failure_category", string(CategoryOf(err))),
				)
				return err
			}

			logger.InfoContext(ctx, "job completed",
				slog.String("task_type", taskType),
				slog.String("task_id", taskID),
				slog.String("queue", queue),
				slog.Int("attempt", retryCount+1),
				slog.Duration("duration", duration),
			)
			return nil
		})
	}
}
