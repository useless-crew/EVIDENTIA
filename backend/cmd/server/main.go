// Command server is Evidentia's backend entrypoint. It is intentionally a
// thin composition root: load config, build the logger, connect
// infrastructure, build the router, serve, and shut down gracefully on
// SIGINT/SIGTERM. No business logic belongs here — see internal/app for the
// dependency container and internal/httpserver for the router.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"evidentia/backend/internal/app"
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

	return app.New(initCtx)
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
