-- Evidentia — Auth Sessions (Up)
--
-- Refresh-token session storage for System 3 (Authentication & Session
-- Security). Never stores a raw refresh token — only a SHA-256 hash of it
-- (see backend/internal/auth/refresh.go). The raw token is high-entropy
-- (256 random bits, base64url-encoded) and single-purpose, so a fast,
-- non-adaptive hash (SHA-256) is appropriate here — unlike passwords,
-- there is no low-entropy brute-force risk to slow down with bcrypt, and
-- a bcrypt lookup on every refresh call would be needlessly expensive.
--
-- No RLS on this table, consistent with System 2's own categorization:
-- users/roles/permissions/user_roles/role_permissions are "identity-plane"
-- reference data protected by grants, not row-level policies, and
-- auth_sessions — being fundamentally a property of a user's identity,
-- not case-scoped evidence — belongs in that same category. It would also
-- create a bootstrap problem: refresh must look up a session by
-- token_hash BEFORE it knows which user is authenticating, so a policy
-- requiring user_id = current_app_user_id() would make that first lookup
-- impossible (the same class of problem System 2 solved for case_members
-- by checking cases.created_by directly instead).

CREATE TABLE auth_sessions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    family_id     UUID NOT NULL,
    token_hash    BYTEA NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at  TIMESTAMPTZ,
    revoked_at    TIMESTAMPTZ,
    replaced_by   UUID REFERENCES auth_sessions(id) ON DELETE SET NULL,
    -- TEXT, not INET: this column is diagnostic/audit metadata only (no
    -- CIDR matching or other network-address logic is ever performed on
    -- it), and Gin's c.ClientIP() already returns a plain string — storing
    -- TEXT avoids a netip.Addr parse/convert step for no functional benefit.
    ip_address    TEXT,
    user_agent    TEXT,

    CONSTRAINT auth_sessions_token_hash_unique UNIQUE (token_hash),
    CONSTRAINT auth_sessions_token_hash_length_check CHECK (octet_length(token_hash) = 32)
);

COMMENT ON TABLE auth_sessions IS
    'Refresh-token sessions. token_hash is SHA-256(raw token) — the raw '
    'token itself is never persisted. family_id groups a chain of rotated '
    'tokens descending from one login: on rotation the new row keeps the '
    'same family_id as its parent, so reuse of a already-rotated '
    '(revoked) token can invalidate the whole family, not just the one '
    'token presented (see internal/service/auth_service.go). Sessions are '
    'ended via UPDATE (revoked_at), never DELETE — consistent with this '
    'schema''s soft-lifecycle convention elsewhere. ON DELETE CASCADE from '
    'users is deliberate here (unlike the RESTRICT used for evidence '
    'tables in System 2): a session has no independent evidentiary value, '
    'so removing a user''s sessions when the user itself is removed is '
    'correct, not a data-loss risk.';
COMMENT ON COLUMN auth_sessions.family_id IS
    'Groups a chain of rotated refresh tokens. Set to a fresh UUID at '
    'login; carried forward unchanged on every rotation descending from '
    'that login.';

CREATE INDEX idx_auth_sessions_user_id ON auth_sessions(user_id);
CREATE INDEX idx_auth_sessions_family_id ON auth_sessions(family_id);
CREATE INDEX idx_auth_sessions_expires_at ON auth_sessions(expires_at);
CREATE INDEX idx_auth_sessions_revoked_at ON auth_sessions(revoked_at);

-- Runtime privileges: read/write, but never delete — a session's history
-- (including revoked/replaced rows) stays available for reuse-detection
-- and incident review, matching this schema's append/update-only pattern
-- for security-relevant tables.
GRANT SELECT, INSERT, UPDATE ON auth_sessions TO evidentia_app;
