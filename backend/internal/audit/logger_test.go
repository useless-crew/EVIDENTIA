package audit

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSlogRecorder_RecordsExpectedFields(t *testing.T) {
	var buf bytes.Buffer
	r := NewSlogRecorder(slog.New(slog.NewJSONHandler(&buf, nil)))

	userID := uuid.New()
	r.Record(context.Background(), Event{
		Action:       "AUTH_LOGIN_FAILED",
		ResourceType: "user",
		UserID:       &userID,
		Role:         "LAWYER",
		Metadata:     map[string]any{"reason": "invalid_credentials"},
	})

	out := buf.String()
	assert.Contains(t, out, `"action":"AUTH_LOGIN_FAILED"`)
	assert.Contains(t, out, `"resource_type":"user"`)
	assert.Contains(t, out, userID.String())
	assert.Contains(t, out, `"role":"LAWYER"`)
	assert.Contains(t, out, "invalid_credentials")
}

func TestSlogRecorder_NeverPanicsOnEmptyEvent(t *testing.T) {
	var buf bytes.Buffer
	r := NewSlogRecorder(slog.New(slog.NewJSONHandler(&buf, nil)))

	require.NotPanics(t, func() {
		r.Record(context.Background(), Event{Action: "AUTH_LOGOUT", ResourceType: "session"})
	})
	assert.Contains(t, buf.String(), "AUTH_LOGOUT")
}
