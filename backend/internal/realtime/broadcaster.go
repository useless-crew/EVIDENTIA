package realtime

import (
	"sync"

	"github.com/google/uuid"
)

// subscriberBufferSize bounds how many un-delivered events a single slow
// SSE client can accumulate before Publish starts dropping its oldest
// undelivered events rather than blocking. A client that misses one is
// never left permanently out of date: it always receives the next event,
// and — critically — the REST status endpoint (never this in-memory
// buffer) is the authoritative source of "what is the CURRENT state"
// after a reconnect (see docs/AUDIT_CHAIN.md's "SSE" section). Verified
// progress events are throttled to roughly one per batch (see
// internal/service.AuditService's progress-update cadence), so in
// practice this buffer is never anywhere near full during normal
// operation.
const subscriberBufferSize = 8

// Broadcaster fans out VerificationEvents to every currently-connected SSE
// client for a given verification ID. It is the ONLY coupling between the
// background worker (internal/service.AuditService.RunVerification, which
// calls Publish) and an HTTP-serving goroutine (sse.go's handler, which
// calls Subscribe) — Publish NEVER blocks on a slow or absent subscriber
// (see subscriberBufferSize above), so a stalled SSE client can never stall
// the verification worker itself, and the worker holds no database
// transaction open while any of this happens (Publish is called AFTER a
// batch's own transaction has already committed — see AuditService).
type Broadcaster struct {
	mu   sync.Mutex
	subs map[uuid.UUID]map[chan VerificationEvent]struct{}
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subs: make(map[uuid.UUID]map[chan VerificationEvent]struct{})}
}

// Subscribe registers a new listener for verificationID and returns the
// channel to read from plus an unsubscribe function the caller MUST defer
// exactly once (see sse.go) — failing to call it leaks both the channel
// and this Broadcaster's own bookkeeping for it.
func (b *Broadcaster) Subscribe(verificationID uuid.UUID) (<-chan VerificationEvent, func()) {
	ch := make(chan VerificationEvent, subscriberBufferSize)

	b.mu.Lock()
	if b.subs[verificationID] == nil {
		b.subs[verificationID] = make(map[chan VerificationEvent]struct{})
	}
	b.subs[verificationID][ch] = struct{}{}
	b.mu.Unlock()

	unsubscribe := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if set, ok := b.subs[verificationID]; ok {
			delete(set, ch)
			if len(set) == 0 {
				delete(b.subs, verificationID)
			}
		}
		// Deliberately NOT close(ch): Publish snapshots its channel list
		// under b.mu, then sends AFTER releasing the lock (see Publish's
		// own comment for why) — closing ch here could race with a
		// Publish call already mid-flight with a stale snapshot that
		// still includes it, which would panic ("send on closed
		// channel"), not merely race harmlessly. An unreferenced,
		// never-closed channel is ordinary, garbage-collectable Go: once
		// this map entry is deleted, nothing but a possibly-still-
		// in-flight Publish call holds a reference, and that reference
		// drops as soon as that one call returns.
	}
	return ch, unsubscribe
}

// Publish fans event out to every current subscriber of event.
// VerificationID. Never blocks: a subscriber whose buffer is already full
// simply misses this one event (see subscriberBufferSize) rather than
// backpressuring the publisher — this method has no error return because
// "nobody is listening right now" is an entirely normal, expected state
// (e.g. between an SSE client disconnecting and reconnecting), never a
// failure.
func (b *Broadcaster) Publish(event VerificationEvent) {
	b.mu.Lock()
	subs := b.subs[event.VerificationID]
	// Snapshot the channel list under the lock, then send outside it —
	// sending while holding the lock would let one slow/blocked receiver
	// (impossible today given the buffered, non-blocking send below, but
	// kept as a structural guarantee against a future change reintroducing
	// blocking sends) stall Subscribe/Publish calls for every OTHER
	// verification too.
	chans := make([]chan VerificationEvent, 0, len(subs))
	for ch := range subs {
		chans = append(chans, ch)
	}
	b.mu.Unlock()

	for _, ch := range chans {
		select {
		case ch <- event:
		default:
			// Buffer full — drop rather than block (see this type's own
			// doc comment). The client's next REST poll or the next
			// throttled event will carry it current again.
		}
	}
}
