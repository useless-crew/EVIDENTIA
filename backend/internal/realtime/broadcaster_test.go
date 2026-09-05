package realtime

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBroadcaster_SubscribeReceivesPublishedEvent(t *testing.T) {
	b := NewBroadcaster()
	id := uuid.New()

	ch, unsubscribe := b.Subscribe(id)
	defer unsubscribe()

	want := VerificationEvent{Type: EventVerificationProgress, VerificationID: id, EntriesChecked: 5}
	b.Publish(want)

	select {
	case got := <-ch:
		assert.Equal(t, want, got)
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive the published event")
	}
}

func TestBroadcaster_PublishWithNoSubscribersDoesNotBlockOrPanic(t *testing.T) {
	b := NewBroadcaster()
	assert.NotPanics(t, func() {
		b.Publish(VerificationEvent{VerificationID: uuid.New()})
	})
}

func TestBroadcaster_EventsAreScopedToTheirOwnVerificationID(t *testing.T) {
	b := NewBroadcaster()
	idA, idB := uuid.New(), uuid.New()

	chA, unsubA := b.Subscribe(idA)
	defer unsubA()
	chB, unsubB := b.Subscribe(idB)
	defer unsubB()

	b.Publish(VerificationEvent{VerificationID: idA, EntriesChecked: 1})

	select {
	case got := <-chA:
		assert.Equal(t, idA, got.VerificationID)
	case <-time.After(time.Second):
		t.Fatal("subscriber A did not receive its own event")
	}

	select {
	case <-chB:
		t.Fatal("subscriber B must never receive an event published for a different verification id")
	case <-time.After(50 * time.Millisecond):
		// Expected: nothing arrives.
	}
}

func TestBroadcaster_UnsubscribeStopsDelivery(t *testing.T) {
	b := NewBroadcaster()
	id := uuid.New()

	ch, unsubscribe := b.Subscribe(id)
	unsubscribe()

	// Deliberately NOT closed by unsubscribe (see that function's own doc
	// comment — closing here could race with an in-flight Publish call
	// and panic). "Stops delivery" means a LATER Publish call never
	// reaches this channel, not that the channel itself becomes closed.
	b.Publish(VerificationEvent{VerificationID: id})

	select {
	case _, ok := <-ch:
		t.Fatalf("unsubscribed channel must never receive a later-published event (ok=%v)", ok)
	case <-time.After(50 * time.Millisecond):
		// Expected: nothing arrives, ever.
	}
}

// TestBroadcaster_PublishNeverBlocksOnASlowSubscriber is the mandatory
// "the verification worker must remain decoupled from the SSE
// connection" guarantee: a subscriber that never reads must never stall
// Publish, however many events are sent.
func TestBroadcaster_PublishNeverBlocksOnASlowSubscriber(t *testing.T) {
	b := NewBroadcaster()
	id := uuid.New()

	_, unsubscribe := b.Subscribe(id) // never read from
	defer unsubscribe()

	done := make(chan struct{})
	go func() {
		for i := 0; i < subscriberBufferSize*10; i++ {
			b.Publish(VerificationEvent{VerificationID: id, EntriesChecked: int64(i)})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a slow/absent-reading subscriber")
	}
}

func TestBroadcaster_ConcurrentSubscribeAndPublish(t *testing.T) {
	b := NewBroadcaster()
	id := uuid.New()

	var wg sync.WaitGroup
	wg.Add(20)
	for i := 0; i < 10; i++ {
		go func() {
			defer wg.Done()
			ch, unsubscribe := b.Subscribe(id)
			defer unsubscribe()
			select {
			case <-ch:
			case <-time.After(time.Second):
			}
		}()
	}
	for i := 0; i < 10; i++ {
		go func(n int) {
			defer wg.Done()
			b.Publish(VerificationEvent{VerificationID: id, EntriesChecked: int64(n)})
		}(i)
	}
	wg.Wait()
}

func TestVerificationEvent_Terminal(t *testing.T) {
	cases := []struct {
		eventType string
		terminal  bool
	}{
		{EventVerificationStarted, false},
		{EventVerificationProgress, false},
		{EventVerificationCompleted, true},
		{EventVerificationIntegrityFailure, true},
		{EventVerificationFailed, true},
	}
	for _, tc := range cases {
		event := VerificationEvent{Type: tc.eventType}
		require.Equal(t, tc.terminal, event.Terminal(), tc.eventType)
	}
}
