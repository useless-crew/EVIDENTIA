-- Evidentia — Auth Session (Refresh Token) Queries
--
-- token_hash is always SHA-256(raw refresh token) — the raw token itself
-- is never a column here and never passed to these queries. See
-- internal/auth/refresh.go and internal/service/auth_service.go.

-- name: CreateAuthSession :one
INSERT INTO auth_sessions (user_id, family_id, token_hash, expires_at, ip_address, user_agent)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, user_id, family_id, token_hash, expires_at, created_at, last_used_at, revoked_at, replaced_by, ip_address, user_agent;

-- name: GetAuthSessionByTokenHash :one
SELECT id, user_id, family_id, token_hash, expires_at, created_at, last_used_at, revoked_at, replaced_by, ip_address, user_agent
FROM auth_sessions
WHERE token_hash = $1;

-- name: RevokeAuthSessionAndReplace :exec
-- Marks a session used and revoked in the same step, recording which new
-- session replaced it — the "rotation" half of refresh-token rotation.
UPDATE auth_sessions
SET revoked_at = now(), last_used_at = now(), replaced_by = $2
WHERE id = $1 AND revoked_at IS NULL;

-- name: RevokeAuthSession :exec
-- Plain revocation with no replacement — used by logout.
UPDATE auth_sessions
SET revoked_at = now()
WHERE id = $1 AND revoked_at IS NULL;

-- name: RevokeAuthSessionFamily :exec
-- Invalidates every still-active session descending from the same login
-- as sessionID's family — used when a revoked/rotated token is presented
-- again (reuse detection: see master prompt §25).
UPDATE auth_sessions
SET revoked_at = now()
WHERE family_id = $1 AND revoked_at IS NULL;
