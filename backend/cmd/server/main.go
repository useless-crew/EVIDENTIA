// Command server is Evidentia's backend entrypoint. It is intentionally a
// thin composition root: load config, build the logger, connect
// infrastructure, build the router, serve, and shut down gracefully on
// SIGINT/SIGTERM. No business logic belongs here — see internal/app for the
// dependency container and internal/httpserver for the router.
//
// @title                       Evidentia API
// @version                     0.3.0
// @description                 Secure digital evidence and case-management platform. This spec currently documents only System 3's authentication endpoints — see docs/API_ENDPOINTS.md for the full intended surface, most of which is not implemented yet.
// @BasePath                    /
// @securityDefinitions.apikey  BearerAuth
// @in                          header
// @name                        Authorization
// @description                 Type "Bearer" followed by a space and the access token returned by POST /api/v1/auth/login or /api/v1/auth/refresh.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	"github.com/joho/godotenv"

	"evidentia/backend/internal/app"
	"evidentia/backend/internal/audit"
	"evidentia/backend/internal/bootstrap"
	"evidentia/backend/internal/httpserver"
	"evidentia/backend/internal/jobs"
)

// startupTimeout bounds how long connecting to every infrastructure
// dependency (PostgreSQL, Redis, MinIO) may take before startup fails.
const startupTimeout = 30 * time.Second

func main() {
	// Best-effort local-dev convenience: load a .env file if one exists.
	// In containers and CI, configuration comes from real environment
	// variables and no .env file is present — that is not an error.
	_ = godotenv.Load()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	application, err := startup(ctx)
	if err != nil {
		// The logger may not exist yet if configuration itself failed, so
		// this one message falls back to a bare stderr logger.
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("startup failed", slog.String("error", err.Error()))
		os.Exit(1)
	}

	run(ctx, application)
}

func startup(ctx context.Context) (*app.App, error) {
	initCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()

	application, err := app.New(initCtx)
	if err != nil {
		return nil, err
	}

	// Idempotent — see internal/bootstrap.EnsureBootstrapAdmin's doc
	// comment. Failing this fails startup (fail closed): if an operator
	// set EVIDENTIA_BOOTSTRAP_ADMIN_* they mean for it to take effect, not
	// to be silently skipped on error.
	recorder := audit.NewSlogRecorder(application.Logger)
	if err := bootstrap.EnsureBootstrapAdmin(initCtx, application.DB.Pool(), application.Config.Bootstrap, application.Config.JWT.BcryptCost, recorder, application.Logger); err != nil {
		application.Close()
		return nil, fmt.Errorf("bootstrap admin: %w", err)
	}

	return application, nil
}

// run serves both the HTTP API and System 11's Asynq worker from this ONE
// process/binary — there is no separate "worker" deployment unit in this
// project's docker-compose (see docker-compose.yml: postgres/redis/minio/
// backend, nothing else), and audit-chain verification's own workload
// (a handful of sequential batched reads against the same PostgreSQL pool
// the HTTP server already shares) does not warrant the operational
// complexity of a second container/binary just to run asynq.Server.Run in
// its own process. A future system with a genuinely different scaling
// profile can still introduce `cmd/worker` later without this file's
// HTTP-serving half needing to change at all — jobs.NewServer/NewMux take
// no dependency on httpserver or vice versa.
func run(ctx context.Context, a *app.App) {
	router := httpserver.NewRouter(a)
	server := httpserver.New(a.Config.Server, router)

	redisOpt := asynq.RedisClientOpt{Addr: a.Config.Redis.Addr, Password: a.Config.Redis.Password, DB: a.Config.Redis.DB}
	errorHandler := jobs.NewAuditVerificationErrorHandler(a.AuditService, a.Logger)
	worker := jobs.NewServer(redisOpt, errorHandler, a.Logger)
	mux := jobs.NewMux(jobs.NewAuditVerificationHandler(a.AuditService, a.Logger))

	a.Logger.Info("starting server",
		slog.String("addr", a.Config.Server.Addr()),
		slog.String("env", a.Config.App.Env),
		slog.String("version", a.Config.App.Version),
	)

	serverErr := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	workerErr := make(chan error, 1)
	go func() {
		workerErr <- worker.Run(mux)
	}()

	select {
	case <-ctx.Done():
		a.Logger.Info("shutdown signal received")
	case err := <-serverErr:
		if err != nil {
			a.Logger.Error("server failed", slog.String("error", err.Error()))
		}
	case err := <-workerErr:
		if err != nil {
			a.Logger.Error("audit verification worker failed", slog.String("error", err.Error()))
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.Config.Server.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		a.Logger.Error("graceful http shutdown failed", slog.String("error", err.Error()))
	} else {
		a.Logger.Info("http server stopped accepting requests")
	}

	// Shutdown waits for any in-flight task's ProcessTask to return before
	// stopping — a verification already RUNNING is allowed to reach its
	// own next checkpoint (batch boundary) rather than being killed
	// mid-batch, so it never leaves audit_verifications in a state neither
	// "properly progressed" nor "properly failed".
	worker.Shutdown()
	a.Logger.Info("audit verification worker stopped")

	a.Close()
	a.Logger.Info("shutdown complete")
}
