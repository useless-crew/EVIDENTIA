// Package cache provides the Redis connection infrastructure that later
// systems (Asynq job queues, caching strategies, pub/sub) will build on. No
// caching strategy, queue, or business logic lives here — only
// connectivity, health checking, and graceful shutdown.
package cache

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"

	"evidentia/backend/internal/config"
)

// Cache wraps a Redis client. Callers depend on this type rather than
// importing go-redis directly, keeping the connection lifecycle centrally
// owned by the application container (internal/app).
type Cache struct {
	client *redis.Client
}

// New builds a Redis client per cfg and verifies connectivity with a Ping
// before returning, so an unreachable Redis fails startup immediately.
func New(ctx context.Context, cfg config.RedisConfig) (*Cache, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("cache: ping: %w", err)
	}

	return &Cache{client: client}, nil
}

// Client exposes the underlying go-redis client for later systems (e.g.
// Asynq, which needs its own *redis.Client-compatible connection options).
func (c *Cache) Client() *redis.Client {
	return c.client
}

// Ping verifies Redis is currently reachable. Used by the readiness
// endpoint; ctx should carry a short, request-scoped timeout.
func (c *Cache) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

// Close releases the client's connection pool. Safe to call once during
// graceful shutdown.
func (c *Cache) Close() error {
	return c.client.Close()
}
