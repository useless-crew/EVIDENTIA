// Package database provides the PostgreSQL connection-pool infrastructure
// that later systems' repositories will build on. It intentionally stops at
// connectivity, pooling, and health checking — no schema, no RLS policies,
// no business queries. Those belong to the systems that own them.
package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"evidentia/backend/internal/config"
)

// Database wraps a pgx connection pool. Callers depend on this type (via
// dependency injection through the application container, internal/app)
// rather than importing pgxpool directly, so the connection lifecycle has
// exactly one owner.
type Database struct {
	pool *pgxpool.Pool
}

// New establishes a connection pool per cfg and verifies connectivity with
// a Ping before returning, so a misconfigured or unreachable database fails
// application startup immediately rather than surfacing on the first query.
func New(ctx context.Context, cfg config.DatabaseConfig) (*Database, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("database: parse pool config: %w", err)
	}

	poolCfg.MaxConns = int32(cfg.MaxOpenConns)
	// pgxpool has no direct "max idle connections" concept the way
	// database/sql does; MinConns (connections the pool keeps warm) is the
	// closest equivalent and is what DATABASE_MAX_IDLE_CONNS controls here.
	poolCfg.MinConns = int32(cfg.MaxIdleConns)
	poolCfg.MaxConnLifetime = cfg.ConnMaxLifetime

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("database: create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database: ping: %w", err)
	}

	return &Database{pool: pool}, nil
}

// Pool exposes the underlying pgx pool for repositories/sqlc-generated code
// in later systems. Kept as an explicit accessor (not an embedded field) so
// call sites are obviously reaching into the pool rather than treating
// *Database itself as a query executor.
func (d *Database) Pool() *pgxpool.Pool {
	return d.pool
}

// Ping verifies the database is currently reachable. Used by the readiness
// endpoint; ctx should carry a short, request-scoped timeout so a stalled
// database cannot hang /ready indefinitely.
func (d *Database) Ping(ctx context.Context) error {
	return d.pool.Ping(ctx)
}

// Close releases all pooled connections. Safe to call once during graceful
// shutdown; blocks until in-flight connections are returned to the pool.
func (d *Database) Close() {
	d.pool.Close()
}
