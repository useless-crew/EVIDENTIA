package authz

import (
	"testing"

	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/models"
)

func TestIsAdmin(t *testing.T) {
	cases := []struct {
		name  string
		roles []string
		want  bool
	}{
		{"no roles", nil, false},
		{"admin present among several", []string{models.RoleAdmin, models.RoleLawyer}, true},
		{"admin absent", []string{models.RoleLawyer, models.RolePolice}, false},
		{"case-sensitive, no partial match", []string{"admin"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isAdmin(auth.AuthenticatedUser{Roles: c.roles})
			if got != c.want {
				t.Fatalf("isAdmin(%v) = %v, want %v", c.roles, got, c.want)
			}
		})
	}
}

func TestEffectiveRole(t *testing.T) {
	if got := effectiveRole(auth.AuthenticatedUser{}); got != "" {
		t.Fatalf("expected empty effective role for a user with no roles, got %q", got)
	}

	// Roles arrive pre-sorted by name (see ListRolesForUser's ORDER BY),
	// which already places ADMIN first alphabetically whenever present —
	// effectiveRole must not need its own admin special-case to get this
	// right.
	got := effectiveRole(auth.AuthenticatedUser{Roles: []string{models.RoleAdmin, models.RoleLawyer}})
	if got != models.RoleAdmin {
		t.Fatalf("expected ADMIN as the effective role, got %q", got)
	}

	got = effectiveRole(auth.AuthenticatedUser{Roles: []string{models.RoleLawyer}})
	if got != models.RoleLawyer {
		t.Fatalf("expected the user's sole role, got %q", got)
	}
}
