package sse

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"evidentia/backend/internal/events"
)

func newTestManager() *Manager {
	return NewManager(nil, discardLogger())
}

func TestManager_RegisterReceivesDispatchedEvent(t *testing.T) {
	m := newTestManager()
	user := uuid.New()
	ch, unsubscribe, err := m.Register(user, "case:1")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer unsubscribe()

	event := events.Event{EventID: uuid.New(), EventType: "SHARE_CREATED", ResourceType: "case", ResourceID: "1", Data: json.RawMessage(`{}`)}
	m.dispatch(event)

	select {
	case got := <-ch:
		if got.EventID != event.EventID {
			t.Fatalf("received event_id %v, want %v", got.EventID, event.EventID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for dispatched event")
	}
}

func TestManager_EventsAreScopedToTheirOwnResource(t *testing.T) {
	m := newTestManager()
	user := uuid.New()
	chA, unsubA, err := m.Register(user, "case:A")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer unsubA()
	chB, unsubB, err := m.Register(user, "case:B")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer unsubB()

	m.dispatch(events.Event{EventID: uuid.New(), EventType: "SHARE_CREATED", ResourceType: "case", ResourceID: "A", Data: json.RawMessage(`{}`)})

	select {
	case <-chA:
	case <-time.After(time.Second):
		t.Fatal("case A's own subscriber never received the event for case A")
	}
	select {
	case <-chB:
		t.Fatal("case B's subscriber must never receive an event scoped to case A — this would be a cross-resource/cross-agency leak")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestManager_UnsubscribeStopsDelivery(t *testing.T) {
	m := newTestManager()
	user := uuid.New()
	ch, unsubscribe, err := m.Register(user, "case:1")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	unsubscribe()

	m.dispatch(events.Event{EventID: uuid.New(), EventType: "SHARE_CREATED", ResourceType: "case", ResourceID: "1", Data: json.RawMessage(`{}`)})

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("an unsubscribed channel must never receive a further event")
		}
	case <-time.After(100 * time.Millisecond):
		// No event and no close — acceptable (see Register's own doc
		// comment on why the channel is never closed).
	}
}

func TestManager_UnsubscribeIsSafeToCallTwice(t *testing.T) {
	m := newTestManager()
	user := uuid.New()
	_, unsubscribe, err := m.Register(user, "case:1")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	unsubscribe()
	unsubscribe() // must not panic or double-decrement perUser
}

func TestManager_DispatchWithNoSubscribersDoesNotBlockOrPanic(t *testing.T) {
	m := newTestManager()
	m.dispatch(events.Event{EventID: uuid.New(), EventType: "SHARE_CREATED", ResourceType: "case", ResourceID: "nobody-subscribed", Data: json.RawMessage(`{}`)})
}

func TestManager_SlowSubscriberDropsRatherThanBlocks(t *testing.T) {
	m := newTestManager()
	user := uuid.New()
	ch, unsubscribe, err := m.Register(user, "case:1")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer unsubscribe()

	// Fill the buffer well past capacity without ever reading — dispatch
	// must never block on this slow consumer.
	done := make(chan struct{})
	go func() {
		for i := 0; i < subscriberBufferSize*4; i++ {
			m.dispatch(events.Event{EventID: uuid.New(), EventType: "SHARE_CREATED", ResourceType: "case", ResourceID: "1", Data: json.RawMessage(`{}`)})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch blocked on a slow/non-reading subscriber — must drop instead")
	}
	// Drain whatever made it into the bounded buffer — never more than
	// subscriberBufferSize.
	drained := 0
	for {
		select {
		case <-ch:
			drained++
		default:
			if drained > subscriberBufferSize {
				t.Fatalf("drained %d events, buffer bound is %d — unbounded queue growth", drained, subscriberBufferSize)
			}
			return
		}
	}
}

func TestManager_RegisterEnforcesPerUserConnectionLimit(t *testing.T) {
	m := newTestManager()
	user := uuid.New()
	var unsubs []func()
	for i := 0; i < maxConnectionsPerUser; i++ {
		_, unsubscribe, err := m.Register(user, "case:1")
		if err != nil {
			t.Fatalf("Register #%d: unexpected error %v", i, err)
		}
		unsubs = append(unsubs, unsubscribe)
	}

	_, _, err := m.Register(user, "case:1")
	if err != ErrTooManyConnections {
		t.Fatalf("Register beyond the per-user limit: got err=%v, want ErrTooManyConnections", err)
	}

	// A DIFFERENT user must be unaffected by the first user's limit.
	otherUser := uuid.New()
	_, unsubOther, err := m.Register(otherUser, "case:1")
	if err != nil {
		t.Fatalf("a different user's Register must not be limited by another user's connection count: %v", err)
	}
	unsubOther()

	for _, u := range unsubs {
		u()
	}

	// After releasing every connection, the same user can register again.
	_, unsubscribe, err := m.Register(user, "case:1")
	if err != nil {
		t.Fatalf("Register after releasing every prior connection: %v", err)
	}
	unsubscribe()
}
