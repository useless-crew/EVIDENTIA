// Package audit defines the minimal integration boundary domain code
// (starting with authentication in System 3) depends on to record a
// security-relevant event, plus the hash-chained, tamper-evident
// audit_log writer/verifier that implements it (System 8/10/11 — see
// chain.go, verifier.go, writer.go in this package). Recorder is defined
// as an interface specifically so calling code never had to change when
// those landed: every service depends on this interface, not a concrete
// implementation directly.
package audit

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
)

// Event is a security-relevant occurrence. Fields mirror audit_log's
// columns (see backend/db/migrations/000001_init_schema.up.sql) so a
// future Recorder can persist them directly once System 8 implements the
// hash-chained writer.
type Event struct {
	Action       string
	ResourceType string
	ResourceID   *uuid.UUID
	UserID       *uuid.UUID
	Role         string
	CaseID       *uuid.UUID
	Metadata     map[string]any
}

// Recorder records a security-relevant event. Record does not return an
// error: callers (e.g. a failed login) must never fail the HTTP response
// merely because audit recording had a hiccup — see master prompt §49.
// A future persistent Recorder should apply the same principle internally
// (log its own write failures rather than propagating them), for the same
// reason.
type Recorder interface {
	Record(ctx context.Context, event Event)
}

// SlogRecorder is System 3's temporary Recorder: it writes to the
// structured operational logger, not to the audit_log table. This is NOT
// the durable, tamper-evident audit trail — audit_log's hash-chain
// semantics require System 8's writer, which this type deliberately does
// not attempt to reimplement (master prompt §48: "do not implement the
// hash-chain algorithm here").
type SlogRecorder struct {
	logger *slog.Logger
}

func NewSlogRecorder(logger *slog.Logger) *SlogRecorder {
	return &SlogRecorder{logger: logger}
}

func (r *SlogRecorder) Record(ctx context.Context, event Event) {
	attrs := make([]any, 0, 6+len(event.Metadata))
	attrs = append(attrs,
		slog.String("action", event.Action),
		slog.String("resource_type", event.ResourceType),
	)
	if event.UserID != nil {
		attrs = append(attrs, slog.String("user_id", event.UserID.String()))
	}
	if event.Role != "" {
		attrs = append(attrs, slog.String("role", event.Role))
	}
	if event.ResourceID != nil {
		attrs = append(attrs, slog.String("resource_id", event.ResourceID.String()))
	}
	if event.CaseID != nil {
		attrs = append(attrs, slog.String("case_id", event.CaseID.String()))
	}
	for k, v := range event.Metadata {
		attrs = append(attrs, slog.Any("metadata_"+k, v))
	}

	r.logger.InfoContext(ctx, "audit event", attrs...)
}
