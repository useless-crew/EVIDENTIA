package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// RedisPublisher is the production Publisher — PUBLISHes to Channel on
// the SAME shared *redis.Client every other Redis-backed component in
// this codebase already uses (internal/cache.Cache's connection,
// reused — see internal/app.App's own construction; never a second
// Redis connection pool). Redis here is transport ONLY: it holds no
// durable event history, and this type asserts nothing stronger than
// at-most-once, best-effort delivery (see the events package's own doc
// comment).
type RedisPublisher struct {
	client *redis.Client
	logger *slog.Logger
}

func NewRedisPublisher(client *redis.Client, logger *slog.Logger) *RedisPublisher {
	return &RedisPublisher{client: client, logger: logger}
}

// Publish builds a fresh Event (see buildEvent) and PUBLISHes it as JSON
// to Channel. Never returns an error and never blocks the caller's
// business transaction on Redis being reachable — see Publisher's own
// doc comment for why a failure here is logged, not propagated. Called
// AFTER the triggering database transaction has already committed (every
// call site in internal/service follows this ordering — see
// docs/REALTIME_EVENTS.md's "Database Transaction + Event Publication"),
// so a Redis outage can only ever cause a MISSED notification, never a
// state change the database itself never actually made.
func (p *RedisPublisher) Publish(ctx context.Context, eventType, resourceType, resourceID string, data any) {
	event, payload, err := buildEvent(eventType, resourceType, resourceID, data)
	if err != nil {
		p.logger.ErrorContext(ctx, "events: failed to build event — not published",
			slog.String("event_type", eventType),
			slog.String("resource_type", resourceType),
			slog.String("error", err.Error()),
		)
		return
	}

	if err := p.client.Publish(ctx, Channel, payload).Err(); err != nil {
		p.logger.ErrorContext(ctx, "events: redis publish failed",
			slog.String("event_type", eventType),
			slog.String("event_id", event.EventID.String()),
			slog.String("resource_type", resourceType),
			slog.String("error", err.Error()),
		)
		return
	}

	p.logger.DebugContext(ctx, "event published",
		slog.String("event_type", eventType),
		slog.String("event_id", event.EventID.String()),
		slog.String("resource_type", resourceType),
		slog.String("resource_id", resourceID),
	)
}

// buildEvent assembles the full Event envelope (fresh EventID,
// CurrentEventVersion, a UTC timestamp) and its JSON encoding — separated
// from Publish specifically so this construction/serialization logic is
// unit-testable without a real Redis connection (see redis_publisher_test.go);
// the ONLY thing Publish adds on top is the actual network call.
func buildEvent(eventType, resourceType, resourceID string, data any) (Event, []byte, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return Event{}, nil, fmt.Errorf("events: marshal event data: %w", err)
	}

	event := Event{
		EventID:      uuid.New(),
		EventType:    eventType,
		EventVersion: CurrentEventVersion,
		Timestamp:    time.Now().UTC(),
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Data:         raw,
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return Event{}, nil, fmt.Errorf("events: marshal event envelope: %w", err)
	}
	return event, payload, nil
}
