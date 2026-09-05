package jobs

import (
	"fmt"
	"log/slog"

	"github.com/hibiken/asynq"
)

// serverConcurrency bounds how many tasks this process's Asynq worker runs
// at once. A small, fixed value is deliberate: audit-chain verification is
// I/O-bound against the SAME PostgreSQL pool the HTTP server shares (see
// internal/app.App — the worker is embedded in the same process/binary,
// not a separate deployment unit; see cmd/server/main.go's doc comment for
// why), so a large concurrency value would only contend with foreground
// request traffic for connections, not meaningfully speed up a single
// chain's necessarily-sequential seq-ordered traversal anyway.
const serverConcurrency = 5

// NewServer builds the Asynq worker server against redisOpt (the same
// connection parameters Client uses — see that type's doc comment). It
// does not start processing until Run/Start is called (see
// cmd/server/main.go), so constructing one has no side effect beyond
// preparing a Redis connection pool, mirroring every other infrastructure
// client's constructor in this codebase (internal/cache.New,
// internal/database.New, ...) — except this one specifically does NOT
// verify connectivity eagerly the way those do, since asynq.NewServer has
// no equivalent synchronous health check to call before Run.
func NewServer(redisOpt asynq.RedisConnOpt, errorHandler asynq.ErrorHandler, logger *slog.Logger) *asynq.Server {
	return asynq.NewServer(redisOpt, asynq.Config{
		Concurrency:  serverConcurrency,
		Queues:       map[string]int{"default": 1},
		ErrorHandler: errorHandler,
		Logger:       &slogAdapter{logger: logger},
	})
}

// NewMux registers every System 11 task handler. A later system adding a
// new task type (certificate generation, document processing — see
// TECH_STACK.md's "Async Processing") registers it here too, never a
// second competing asynq.Server/mux.
func NewMux(auditHandler *AuditVerificationHandler) *asynq.ServeMux {
	mux := asynq.NewServeMux()
	mux.Handle(TypeVerifyAuditChain, auditHandler)
	return mux
}

// slogAdapter satisfies asynq's own minimal Logger interface using this
// project's existing structured logger, so worker diagnostics land in the
// same operational log stream (same format/level configuration) as
// everything else — never a second, differently-configured logging path.
type slogAdapter struct {
	logger *slog.Logger
}

func (l *slogAdapter) Debug(args ...interface{}) { l.logger.Debug(fmt.Sprint(args...)) }
func (l *slogAdapter) Info(args ...interface{})  { l.logger.Info(fmt.Sprint(args...)) }
func (l *slogAdapter) Warn(args ...interface{})  { l.logger.Warn(fmt.Sprint(args...)) }
func (l *slogAdapter) Error(args ...interface{}) { l.logger.Error(fmt.Sprint(args...)) }
func (l *slogAdapter) Fatal(args ...interface{}) { l.logger.Error(fmt.Sprint(args...)) }
