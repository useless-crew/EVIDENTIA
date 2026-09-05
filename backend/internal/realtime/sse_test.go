package realtime

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newTestContext(t *testing.T, ctx context.Context) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest("GET", "/events", nil)
	c.Request = req.WithContext(ctx)
	return c, w
}

func TestStreamVerification_SendsInitialEventAndClosesIfTerminal(t *testing.T) {
	c, w := newTestContext(t, context.Background())
	id := uuid.New()
	initial := VerificationEvent{Type: EventVerificationCompleted, VerificationID: id, Status: "VERIFIED"}

	var unsubscribed bool
	ch := make(chan VerificationEvent)
	done := make(chan struct{})
	go func() {
		StreamVerification(c, initial, ch, func() { unsubscribed = true })
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("StreamVerification did not return after a terminal initial event")
	}

	assert.True(t, unsubscribed, "unsubscribe must always be called")
	assert.Contains(t, w.Body.String(), "event: verification_completed")
	assert.Contains(t, w.Body.String(), `"status":"VERIFIED"`)
	assert.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
}

func TestStreamVerification_RelaysEventsUntilTerminal(t *testing.T) {
	c, w := newTestContext(t, context.Background())
	id := uuid.New()
	initial := VerificationEvent{Type: EventVerificationProgress, VerificationID: id, Status: "RUNNING", EntriesChecked: 10}

	ch := make(chan VerificationEvent, 2)
	var unsubscribed bool
	done := make(chan struct{})
	go func() {
		StreamVerification(c, initial, ch, func() { unsubscribed = true })
		close(done)
	}()

	ch <- VerificationEvent{Type: EventVerificationProgress, VerificationID: id, Status: "RUNNING", EntriesChecked: 50}
	ch <- VerificationEvent{Type: EventVerificationCompleted, VerificationID: id, Status: "VERIFIED", EntriesChecked: 100}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("StreamVerification did not close after a terminal event arrived on the channel")
	}

	assert.True(t, unsubscribed)
	body := w.Body.String()
	assert.Equal(t, 2, strings.Count(body, "event: verification_progress"), "initial event + one progress update")
	assert.Equal(t, 1, strings.Count(body, "event: verification_completed"))
}

func TestStreamVerification_ClosesOnClientDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c, _ := newTestContext(t, ctx)
	id := uuid.New()
	initial := VerificationEvent{Type: EventVerificationProgress, VerificationID: id, Status: "RUNNING"}

	ch := make(chan VerificationEvent) // never sends
	var unsubscribed bool
	done := make(chan struct{})
	go func() {
		StreamVerification(c, initial, ch, func() { unsubscribed = true })
		close(done)
	}()

	// Give the goroutine a moment to enter its select loop, then simulate
	// the client disconnecting.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("StreamVerification did not return after client disconnect")
	}
	assert.True(t, unsubscribed, "unsubscribe must be called even on client disconnect — no leaked broadcaster subscription")
}

func TestStreamVerification_ChannelClosedEndsStream(t *testing.T) {
	c, _ := newTestContext(t, context.Background())
	id := uuid.New()
	initial := VerificationEvent{Type: EventVerificationProgress, VerificationID: id, Status: "RUNNING"}

	ch := make(chan VerificationEvent)
	done := make(chan struct{})
	go func() {
		StreamVerification(c, initial, ch, func() {})
		close(done)
	}()

	close(ch)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("StreamVerification did not return after its event channel was closed")
	}
}

func TestStreamVerification_UnsubscribeCalledExactlyOnce(t *testing.T) {
	c, _ := newTestContext(t, context.Background())
	id := uuid.New()
	initial := VerificationEvent{Type: EventVerificationCompleted, VerificationID: id}

	calls := 0
	ch := make(chan VerificationEvent)
	done := make(chan struct{})
	go func() {
		StreamVerification(c, initial, ch, func() { calls++ })
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("did not return")
	}
	require.Equal(t, 1, calls)
}
