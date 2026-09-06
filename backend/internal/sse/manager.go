// Package sse is System 13's ONE Server-Sent Events transport — a
// Redis-Pub/Sub-fed, in-process fan-out (Manager) and the Gin handler
// that streams a registered subscription to an authenticated,
// already-authorized client (Stream, in stream.go). It knows nothing
// about hashing, PostgreSQL, authz, or any specific resource type — a
// caller (internal/handlers/*) authorizes a subscription BEFORE ever
// calling Register (see that method's own doc comment: this package
// enforces no authorization of its own, and must never be mistaken for
// doing so), then this package's only job is delivering whatever
// internal/events.Publisher implementations elsewhere publish to
// whichever locally-connected clients are registered for that exact
// event's ResourceType/ResourceID.
//
// Why ONE Redis subscription per backend process, fanned out in-process,
// rather than one Redis SUBSCRIBE per SSE connection: Manager.Start
// opens exactly one *redis.PubSub against events.Channel per process (see
// manager.go), receiving EVERY event published anywhere; dispatch then
// routes each one, in Go, to only the locally-registered channels whose
// ScopeKey matches — this is what lets any number of connected clients
// share one Redis connection and one JSON-decode per event, and is also
// what makes this design correct across multiple backend replicas (a
// future horizontal-scaling deployment): every replica's own Manager
// receives every event and independently delivers it to only ITS OWN
// locally-connected, already-authorized clients — never a broadcast to
// clients connected to a DIFFERENT replica, and never a per-replica gap
// either, since Redis Pub/Sub fans out to every subscribed process.
package sse

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"evidentia/backend/internal/events"
)

// subscriberBufferSize bounds how many undelivered events a single slow
// SSE client can accumulate before dispatch starts dropping its oldest
// UNSENT event rather than blocking — see dispatch's own doc comment.
// maxConnectionsPerUser bounds how many concurrent SSE connections one
// authenticated user may hold at once — master prompt's own "prevent one
// user from creating unlimited SSE connections"; no rate-limiting
// middleware exists anywhere in this codebase to reuse (see
// docs/BACKGROUND_JOBS.md's own "Rate Limiting" section for the identical
// finding in System 12), so this is a small, self-contained, SSE-specific
// counter — not a general-purpose rate limiter.
const (
	subscriberBufferSize  = 8
	maxConnectionsPerUser = 10
)

// ErrTooManyConnections is Register's error when userID already holds
// maxConnectionsPerUser active subscriptions — the caller (an HTTP
// handler) should translate this into 429 Too Many Requests, never a
// generic 500.
var ErrTooManyConnections = errors.New("sse: too many concurrent connections for this user")

// Manager is the central SSE fan-out — see this package's own doc
// comment. Construct exactly one per process (internal/app.App owns it,
// exactly like it owns the shared Redis/PostgreSQL/MinIO connections);
// every handler that streams SSE registers against this SAME instance,
// never a second Manager.
type Manager struct {
	client *redis.Client
	logger *slog.Logger

	mu      sync.Mutex
	subs    map[string]map[chan events.Event]struct{}
	perUser map[uuid.UUID]int

	done chan struct{}
}

func NewManager(client *redis.Client, logger *slog.Logger) *Manager {
	return &Manager{
		client:  client,
		logger:  logger,
		subs:    make(map[string]map[chan events.Event]struct{}),
		perUser: make(map[uuid.UUID]int),
		done:    make(chan struct{}),
	}
}

// Start subscribes to events.Channel and fans out every received event to
// this process's locally-registered subscribers until ctx is cancelled.
// Intended to run in its own goroutine for the lifetime of the process
// (see cmd/server/main.go), bound to the SAME top-level shutdown context
// every other long-running component (the Asynq worker, the HTTP server)
// already uses. Closes Done() when it returns, so shutdown code can wait
// for the Redis subscription to actually stop before closing the
// underlying Redis client out from under it.
func (m *Manager) Start(ctx context.Context) {
	defer close(m.done)

	pubsub := m.client.Subscribe(ctx, events.Channel)
	defer func() { _ = pubsub.Close() }()

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			var event events.Event
			if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
				// A malformed message on OUR OWN channel would mean a bug in
				// events.RedisPublisher (the only writer to this channel) —
				// log and skip rather than crash the whole fan-out loop over
				// one bad message.
				m.logger.Error("sse: received malformed event from redis — dropped",
					slog.String("error", err.Error()))
				continue
			}
			m.dispatch(event)
		}
	}
}

// Done reports when Start's Redis-subscription goroutine has fully
// exited — see cmd/server/main.go's shutdown sequence for why this
// matters (never close the shared Redis client while Start might still
// be reading from it).
func (m *Manager) Done() <-chan struct{} { return m.done }

// dispatch fans event out to every LOCAL subscriber currently registered
// for its ScopeKey — separated from Start's Redis-receiving loop
// specifically so tests can exercise fan-out/backpressure/scoping logic
// directly (see manager_test.go), without a real Redis connection. Never
// blocks: a registered client whose buffer is already full simply misses
// this one event (master prompt's own "a slow client must not block the
// entire event system" / "bounded buffering") — the client's next REST
// poll, or the resource's own next event, carries it current again
// (System 11's dashboard already relies on exactly this fallback via its
// REST poll timer; see docs/AUDIT_CHAIN.md's "SSE reconnection").
func (m *Manager) dispatch(event events.Event) {
	m.mu.Lock()
	subs := m.subs[event.ScopeKey()]
	chans := make([]chan events.Event, 0, len(subs))
	for ch := range subs {
		chans = append(chans, ch)
	}
	m.mu.Unlock()

	for _, ch := range chans {
		select {
		case ch <- event:
		default:
			// Buffer full — drop rather than block (see this method's own
			// doc comment).
		}
	}
}

// Register subscribes userID to every future event whose
// events.ScopeKey(resourceType, resourceID) matches scopeKey, returning
// the channel to read from and an unsubscribe function the caller MUST
// call exactly once (typically via defer, from Stream — see stream.go).
//
// CRITICAL: Register performs NO authorization of its own. The caller
// MUST have already verified — via the existing RBAC/ABAC/RLS machinery
// (authz.Service.CanAccessCase/CanAccessDocument, or a service method
// like AuditService.GetVerification that itself re-checks both) — that
// userID is allowed to see events for this exact resourceType/resourceID
// BEFORE calling Register. A client can never widen its own scope by
// supplying an arbitrary resourceID: Register trusts scopeKey completely,
// exactly because the caller is responsible for having proven it first —
// this mirrors internal/handlers/audit's own "verification_id in the URL
// is never proof of authorization by itself" posture, generalized to
// every resource type this package will ever serve.
//
// Returns ErrTooManyConnections if userID already holds
// maxConnectionsPerUser active registrations.
func (m *Manager) Register(userID uuid.UUID, scopeKey string) (<-chan events.Event, func(), error) {
	m.mu.Lock()
	if m.perUser[userID] >= maxConnectionsPerUser {
		m.mu.Unlock()
		return nil, nil, ErrTooManyConnections
	}

	ch := make(chan events.Event, subscriberBufferSize)
	if m.subs[scopeKey] == nil {
		m.subs[scopeKey] = make(map[chan events.Event]struct{})
	}
	m.subs[scopeKey][ch] = struct{}{}
	m.perUser[userID]++
	m.mu.Unlock()

	unsubscribe := func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if set, ok := m.subs[scopeKey]; ok {
			if _, present := set[ch]; present {
				delete(set, ch)
				if len(set) == 0 {
					delete(m.subs, scopeKey)
				}
				m.perUser[userID]--
				if m.perUser[userID] <= 0 {
					delete(m.perUser, userID)
				}
			}
		}
		// Deliberately NOT close(ch): dispatch snapshots its channel list
		// under m.mu, then sends AFTER releasing the lock — closing ch here
		// could race with a dispatch call already mid-flight with a stale
		// snapshot that still includes it, which would panic ("send on
		// closed channel"), not merely race harmlessly. An unreferenced,
		// never-closed channel is ordinary, garbage-collectable Go: once
		// this map entry is deleted, nothing but a possibly-still-in-flight
		// dispatch call holds a reference, and that reference drops as soon
		// as that one call returns. The `present` guard above additionally
		// makes this function safe to call more than once (a defer racing
		// an explicit early call, say) without double-decrementing perUser.
	}
	return ch, unsubscribe, nil
}
