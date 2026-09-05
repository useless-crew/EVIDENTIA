//go:build integration

// Run with: go test -tags=integration ./internal/sse/...
// Requires a reachable Redis (REDIS_ADDR, default localhost:6379) — the
// same docker-compose service every other integration test in this
// repository already depends on. Proves what manager_test.go's direct
// dispatch() calls cannot: that Manager.Start's real Redis subscription
// actually receives what events.RedisPublisher actually publishes, over
// the real wire, end to end.
package sse

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/events"
)

func redisClientFromEnv(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	require.NoError(t, client.Ping(context.Background()).Err())
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestManager_ReceivesRealRedisPublishedEvent(t *testing.T) {
	client := redisClientFromEnv(t)
	manager := NewManager(client, discardLogger())
	publisher := events.NewRedisPublisher(client, discardLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go manager.Start(ctx)

	// Give the subscription a moment to actually attach before publishing
	// — Redis Pub/Sub only delivers to subscribers already attached at
	// publish time (the same ephemeral-delivery caveat this whole system
	// documents), so a real subscriber-attach race is exactly what this
	// test would otherwise flake on.
	time.Sleep(200 * time.Millisecond)

	userID := uuid.New()
	scopeID := uuid.New().String()
	ch, unsubscribe, err := manager.Register(userID, events.ScopeKey(events.ResourceTypeCase, scopeID))
	require.NoError(t, err)
	defer unsubscribe()

	publisher.Publish(context.Background(), events.TypeShareCreated, events.ResourceTypeCase, scopeID, events.ShareEventData{ShareID: "s1", DocumentID: "d1", CaseID: scopeID})

	select {
	case event := <-ch:
		require.Equal(t, events.TypeShareCreated, event.EventType)
		require.Equal(t, events.ResourceTypeCase, event.ResourceType)
		require.Equal(t, scopeID, event.ResourceID)
		require.NotEqual(t, uuid.Nil, event.EventID)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the real Redis-published event to reach the registered subscriber")
	}

	cancel()
	select {
	case <-manager.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Manager.Start did not exit after its context was cancelled")
	}
}

func TestManager_DoesNotDeliverToUnrelatedScope(t *testing.T) {
	client := redisClientFromEnv(t)
	manager := NewManager(client, discardLogger())
	publisher := events.NewRedisPublisher(client, discardLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go manager.Start(ctx)
	time.Sleep(200 * time.Millisecond)

	userID := uuid.New()
	unrelatedScope := uuid.New().String()
	ch, unsubscribe, err := manager.Register(userID, events.ScopeKey(events.ResourceTypeCase, unrelatedScope))
	require.NoError(t, err)
	defer unsubscribe()

	publisher.Publish(context.Background(), events.TypeShareCreated, events.ResourceTypeCase, uuid.New().String(), events.ShareEventData{})

	select {
	case event := <-ch:
		t.Fatalf("received an event for an unrelated resource scope — cross-resource leak: %+v", event)
	case <-time.After(1 * time.Second):
	}
}
