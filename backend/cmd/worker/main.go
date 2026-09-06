// Command worker is Evidentia's standalone background-worker entrypoint
// (System 17 — production deployment). It runs ONLY System 11's Asynq
// audit-chain-verification worker: no HTTP server, no SSE fan-out.
//
// This is NOT a second implementation of the worker — it composes the
// exact same internal/app.App, jobs.NewServer, and jobs.NewMux that
// cmd/server/main.go's embedded worker uses, just without also starting
// the HTTP listener. cmd/server remains the default, combined-process
// entrypoint for local development and the base docker-compose.yml; this
// binary exists so docker-compose.prod.yml can run the worker as an
// independently restartable/scalable container (see that file, and
// cmd/server/main.go's DISABLE_EMBEDDED_WORKER doc comment for how the
// two compose to avoid double-consuming the same Asynq queue).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	"github.com/joho/godotenv"

	"evidentia/backend/internal/app"
	"evidentia/backend/internal/jobs"
)

const startupTimeout = 30 * time.Second

func main() {
	// Best-effort local-dev convenience — see cmd/server/main.go's
	// identical call for why this is not an error when absent.
	_ = godotenv.Load()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	initCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	application, err := app.New(initCtx)
	cancel()
	if err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("startup failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer application.Close()

	run(ctx, application)
}

func run(ctx context.Context, a *app.App) {
	redisOpt := asynq.RedisClientOpt{Addr: a.Config.Redis.Addr, Password: a.Config.Redis.Password, DB: a.Config.Redis.DB}

	// Chain both error handlers — each checks task.Type() before acting, so
	// exactly one fires per task type, mirroring cmd/server/main.go.
	auditErrHandler := jobs.NewAuditVerificationErrorHandler(a.AuditService, a.Logger)
	blockchainErrHandler := jobs.NewBlockchainAnchorErrorHandler(a.BlockchainAnchorService)
	errorHandler := asynq.ErrorHandlerFunc(func(ctx context.Context, task *asynq.Task, err error) {
		auditErrHandler.HandleError(ctx, task, err)
		blockchainErrHandler.HandleError(ctx, task, err)
	})

	worker := jobs.NewServer(redisOpt, errorHandler, a.Logger)
	blockchainHandler := jobs.NewBlockchainAnchorHandler(a.BlockchainAnchorService)
	mux := jobs.NewMux(a.Logger, jobs.NewAuditVerificationHandler(a.AuditService), blockchainHandler)

	a.Logger.Info("starting standalone background worker",
		slog.String("env", a.Config.App.Env),
		slog.String("version", a.Config.App.Version),
	)

	workerErr := make(chan error, 1)
	go func() {
		workerErr <- worker.Run(mux)
	}()

	select {
	case <-ctx.Done():
		a.Logger.Info("shutdown signal received")
	case err := <-workerErr:
		if err != nil {
			a.Logger.Error("background worker failed", slog.String("error", err.Error()))
		}
	}

	// Same graceful-drain behavior as cmd/server's embedded worker: an
	// in-flight task reaches its own next checkpoint rather than being
	// killed mid-flight.
	worker.Shutdown()
	a.Logger.Info("worker stopped")
}
