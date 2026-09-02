package app

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// DBConn is the subset of *database.Database the rest of the application
// depends on. Declaring it here (the consuming package) rather than a
// concrete struct dependency lets tests substitute a fake connection
// without a real PostgreSQL instance.
type DBConn interface {
	Pool() *pgxpool.Pool
	Ping(ctx context.Context) error
	Close()
}

// CacheConn is the subset of *cache.Cache the rest of the application
// depends on, for the same reason as DBConn.
type CacheConn interface {
	Client() *redis.Client
	Ping(ctx context.Context) error
	Close() error
}
