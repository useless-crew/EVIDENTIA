package repository

import (
	"context"

	"github.com/google/uuid"

	"evidentia/backend/db/generated"
)

// AuthSessionRepo wraps the refresh-token session queries. auth_sessions
// has no RLS (see the migration) — these queries run under whatever
// AppIdentity WithTx was given, which for auth flows is typically the zero
// value, since a session lookup often happens before the caller's
// identity is known (e.g. refresh, which authenticates via the token
// itself, not a prior session).
type AuthSessionRepo struct {
	q *generated.Queries
}

func NewAuthSessionRepo(q *generated.Queries) *AuthSessionRepo {
	return &AuthSessionRepo{q: q}
}

func (r *AuthSessionRepo) Create(ctx context.Context, arg generated.CreateAuthSessionParams) (generated.AuthSession, error) {
	return r.q.CreateAuthSession(ctx, arg)
}

func (r *AuthSessionRepo) GetByTokenHash(ctx context.Context, tokenHash []byte) (generated.AuthSession, error) {
	return r.q.GetAuthSessionByTokenHash(ctx, tokenHash)
}

// RevokeAndReplace marks sessionID used+revoked and records replacedBy as
// its successor — the "rotation" half of refresh-token rotation.
func (r *AuthSessionRepo) RevokeAndReplace(ctx context.Context, sessionID, replacedBy uuid.UUID) error {
	return r.q.RevokeAuthSessionAndReplace(ctx, generated.RevokeAuthSessionAndReplaceParams{
		ID:         sessionID,
		ReplacedBy: &replacedBy,
	})
}

// Revoke plainly revokes sessionID with no replacement — used by logout.
func (r *AuthSessionRepo) Revoke(ctx context.Context, sessionID uuid.UUID) error {
	return r.q.RevokeAuthSession(ctx, sessionID)
}

// RevokeFamily invalidates every still-active session descending from the
// same login as familyID — reuse detection (master prompt §25): presenting
// an already-rotated token again revokes the whole family, not just that
// one token.
func (r *AuthSessionRepo) RevokeFamily(ctx context.Context, familyID uuid.UUID) error {
	return r.q.RevokeAuthSessionFamily(ctx, familyID)
}
