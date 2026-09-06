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
// chain's necessarily-sequential seq-ordered traversal anyway. Every task
// type this package ever adds shares this ONE bounded worker pool — there
// is no per-task-type concurrency escape hatch, which is exactly what
// keeps a future CPU/memory-heavy task type from being able to run
// unboundedly many instances of itself concurrently (master prompt's
// "prevent a malicious or accidental job from exhausting resources").
const serverConcurrency = 5

// Queue names this package's *asynq.Server ever serves, and their relative
// priority weight (asynq.Config.Queues — a HIGHER number means asynq's own
// weighted scheduler pulls from that queue more often, never a hard
// guarantee of starvation-freedom on its own, but the standard Asynq
// idiom for "some work matters more than other work" — see
// https://github.com/hibiken/asynq/wiki/Queue-Priority).
//
// Only two queues exist today because only two are actually justified
// (master prompt: "Only introduce multiple queues if actually useful" /
// "do not create unnecessary queue complexity"): QueueCritical carries
// System 11's audit-chain verification — a security-sensitive task type
// that must never be starved by a future high-volume queue — and
// QueueDefault is reserved for whatever genuinely-async task type this
// project adds next (see docs/BACKGROUND_JOBS.md's "Task Types" for why
// no document-processing task type exists yet). A THIRD, still-empty
// "document" queue was deliberately not added: an unused queue name is
// exactly the "unnecessary complexity" master prompt warns against: it
// costs the weighted scheduler a share of its attention checking a queue
// nothing ever enqueues to, for a task type that does not exist. Add one
// only when a real task type needs it.
const (
	QueueCritical = "critical"
	QueueDefault  = "default"
)

// queuePriorities is NewServer's asynq.Config.Queues value — see the
// constants above for what each queue is for and why only these two exist.
var queuePriorities = map[string]int{
	QueueCritical: 6,
	QueueDefault:  2,
}

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
		Queues:       queuePriorities,
		ErrorHandler: errorHandler,
		Logger:       &slogAdapter{logger: logger},
	})
}

// NewMux registers every task handler this process's worker serves and
// applies LoggingMiddleware to all of them uniformly — a task type's own
// Handler never needs its own start/duration/failure logging (see that
// middleware's own doc comment). A later system adding a new task type
// registers it here too, never a second competing asynq.Server/mux (master
// prompt: "do not introduce ... a second queue system").
func NewMux(logger *slog.Logger, auditHandler *AuditVerificationHandler) *asynq.ServeMux {
	mux := asynq.NewServeMux()
	mux.Use(LoggingMiddleware(logger))
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
