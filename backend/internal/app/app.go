// Package app assembles Evidentia's application container: configuration,
// logger, and infrastructure clients (PostgreSQL, Redis, MinIO), each
// initialized exactly once and passed by dependency injection to whatever
// depends on it — never as package-level globals. Later systems add fields
// here (or take *App as a constructor argument) rather than reaching for
// their own global connections.
package app

import (
	"context"
	"fmt"
	"log/slog"

	"evidentia/backend/internal/cache"
	"evidentia/backend/internal/config"
	"evidentia/backend/internal/database"
	"evidentia/backend/internal/logger"
	"evidentia/backend/internal/storage"
)

// App holds every foundational dependency the HTTP server and, later,
// handlers/services/repositories need. It has exactly one owner (main),
// which is responsible for calling Close during shutdown.
type App struct {
	Config  *config.Config
	Logger  *slog.Logger
	DB      DBConn
	Cache   CacheConn
	Storage storage.Storage
}

// New loads configuration and connects every infrastructure dependency in
// turn, failing fast (and unwinding anything already connected) if any step
// fails — the application must never start in a partially initialized
// state. ctx bounds how long each connection attempt may take.
func New(ctx context.Context) (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}

	log, err := logger.New(cfg.Logging.Level, cfg.Logging.Format)
	if err != nil {
		return nil, fmt.Errorf("app: build logger: %w", err)
	}

	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		return nil, fmt.Errorf("app: connect database: %w", err)
	}

	redisCache, err := cache.New(ctx, cfg.Redis)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("app: connect redis: %w", err)
	}

	objectStorage, err := storage.NewMinIO(ctx, cfg.MinIO)
	if err != nil {
		db.Close()
		_ = redisCache.Close()
		return nil, fmt.Errorf("app: connect storage: %w", err)
	}

	return &App{
		Config:  cfg,
		Logger:  log,
		DB:      db,
		Cache:   redisCache,
		Storage: objectStorage,
	}, nil
}

// Close releases every infrastructure dependency. Called once during
// graceful shutdown, after the HTTP server has stopped accepting new
// requests (see cmd/server/main.go for the full shutdown sequence).
func (a *App) Close() {
	if err := a.Cache.Close(); err != nil {
		a.Logger.Error("shutdown: closing redis", slog.String("error", err.Error()))
	} else {
		a.Logger.Info("shutdown: redis closed")
	}

	// MinIO's client is a thin HTTP wrapper with no persistent connection
	// pool of its own to release explicitly; nothing to do here beyond
	// acknowledging the shutdown step exists in the sequence.

	a.DB.Close()
	a.Logger.Info("shutdown: database pool closed")
}
