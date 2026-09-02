package authz

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"evidentia/backend/internal/audit"
	"evidentia/backend/internal/auth"
)

// noopRecorder discards every event — sufficient for tests that only care
// about the returned Decision/error, not what got audited.
type noopRecorder struct{}

func (noopRecorder) Record(context.Context, audit.Event) {}

// TestHasPermission_NoRolesFailsClosedWithoutDBAccess passes a nil pool:
// if HasPermission tried to use it for a user with no roles, this test
// would panic on the nil pointer — proving the empty-roles case is truly
// short-circuited before any database access is attempted (master prompt
// §17).
func TestHasPermission_NoRolesFailsClosedWithoutDBAccess(t *testing.T) {
	s := NewService(nil, noopRecorder{})
	allowed, err := s.HasPermission(context.Background(), auth.AuthenticatedUser{}, ActionCaseCreate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Fatal("a user with no roles must never be granted any permission")
	}
}

func TestCanAccessCase_NoRolesDeniesWithoutDBAccess(t *testing.T) {
	s := NewService(nil, noopRecorder{})
	decision, err := s.CanAccessCase(context.Background(), auth.AuthenticatedUser{}, uuid.New(), ActionCaseRead)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Allowed {
		t.Fatal("a user with no roles must never be granted case access")
	}
}

func TestCanAccessDocument_NoRolesDeniesWithoutDBAccess(t *testing.T) {
	s := NewService(nil, noopRecorder{})
	decision, err := s.CanAccessDocument(context.Background(), auth.AuthenticatedUser{}, uuid.New(), ActionDocumentRead)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Allowed {
		t.Fatal("a user with no roles must never be granted document access")
	}
}

func TestCanModifyUserRole_NoRolesDeniesWithoutDBAccess(t *testing.T) {
	s := NewService(nil, noopRecorder{})
	decision, err := s.CanModifyUserRole(context.Background(), auth.AuthenticatedUser{}, uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Allowed {
		t.Fatal("a user with no roles must never be granted role-modification access")
	}
}
