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

	"github.com/joho/godotenv"

	"evidentia/backend/internal/app"
	"evidentia/backend/internal/audit"
	"evidentia/backend/internal/bootstrap"
	"evidentia/backend/internal/httpserver"
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

func run(ctx context.Context, a *app.App) {
	router := httpserver.NewRouter(a)
	server := httpserver.New(a.Config.Server, router)

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

	select {
	case <-ctx.Done():
		a.Logger.Info("shutdown signal received")
	case err := <-serverErr:
		if err != nil {
			a.Logger.Error("server failed", slog.String("error", err.Error()))
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.Config.Server.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		a.Logger.Error("graceful http shutdown failed", slog.String("error", err.Error()))
	} else {
		a.Logger.Info("http server stopped accepting requests")
	}

	a.Close()
	a.Logger.Info("shutdown complete")
}
