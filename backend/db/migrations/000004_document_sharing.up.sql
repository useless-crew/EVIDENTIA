-- Evidentia — Secure Document Sharing & Access Delegation (Up)
--
-- Adds explicit, scoped, revocable, time-bounded document-level access
-- delegation: an authorized user (one who already holds document:share
-- for a document — see internal/authz's CanAccessDocument) grants a
-- SPECIFIC other user VIEW or VERIFY access to a SPECIFIC document,
-- optionally expiring, always revocable, never transferring ownership.
--
-- This does NOT replace or weaken the existing case/document RLS
-- boundary — it adds a SECOND, narrower authorization path alongside it.
-- A recipient's access is granted only for the exact document_id named in
-- an ACTIVE, unexpired share row; it is never a case-wide or account-wide
-- grant, and it never implies RESHARE/REDACT/DELETE/ownership-change
-- capability (see documents_select's ALTER POLICY below, and
-- internal/authz/share_policy.go for the identical application-layer
-- check).
--
-- Permission is deliberately just VIEW/VERIFY, not a third DOWNLOAD tier:
-- this application has no distinct "view metadata without downloading
-- bytes" capability (there is no inline document renderer — see
-- docs/SECURITY.md's "Document Sharing" for the full rationale), so VIEW
-- already covers both read and download, and VERIFY is a strict superset
-- adding document:verify. Certificate access follows VIEW too (a
-- certificate is no more sensitive than the hash it already contains).

-- =============================================================================
-- 1. DOCUMENT_SHARES
-- =============================================================================

CREATE TABLE document_shares (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id         UUID NOT NULL REFERENCES documents(id) ON DELETE RESTRICT,
    shared_with_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_by_user_id  UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    permission          TEXT NOT NULL DEFAULT 'VIEW',
    status              TEXT NOT NULL DEFAULT 'ACTIVE',
    expires_at          TIMESTAMPTZ,
    reason              TEXT,
    metadata            JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at          TIMESTAMPTZ,
    revoked_by_user_id  UUID REFERENCES users(id) ON DELETE RESTRICT,

    CONSTRAINT document_shares_permission_check CHECK (permission IN ('VIEW', 'VERIFY')),
    CONSTRAINT document_shares_status_check CHECK (status IN ('ACTIVE', 'REVOKED')),
    -- Master prompt §33: self-share is rejected, not just discouraged —
    -- enforced at the database level, not only in application code.
    CONSTRAINT document_shares_not_self_check CHECK (shared_with_user_id <> created_by_user_id),
    -- A share's revocation fields are all-or-nothing: REVOKED always
    -- carries who/when; ACTIVE never does. Prevents a half-revoked row
    -- (e.g. revoked_at set but status still ACTIVE) from ever existing.
    CONSTRAINT document_shares_revoked_consistency_check CHECK (
        (status = 'ACTIVE' AND revoked_at IS NULL AND revoked_by_user_id IS NULL)
        OR (status = 'REVOKED' AND revoked_at IS NOT NULL AND revoked_by_user_id IS NOT NULL)
    )
);

COMMENT ON TABLE document_shares IS
    'Explicit, document-scoped access delegation — master prompt §4/§5. A '
    'row grants shared_with_user_id controlled access to EXACTLY '
    'document_id (never the whole case, never every derivative of it — '
    'see docs/SECURITY.md''s "Sharing lineage") for as long as status = '
    '''ACTIVE'' and (expires_at IS NULL OR expires_at > now()). Revocation '
    'is a status transition (ACTIVE -> REVOKED), never a DELETE — no '
    'DELETE privilege is granted below and no DELETE query exists, so '
    'historical delegation/accountability records are permanent, exactly '
    'like redactions/compliance_certificates. The recipient never becomes '
    'the document''s owner/uploader; this table only ever grants a '
    'read-oriented capability (VIEW or VERIFY) checked by '
    'internal/authz.Service.CanAccessDocument alongside — never instead '
    'of — the existing RBAC/ABAC/RLS checks.';
COMMENT ON COLUMN document_shares.permission IS
    'VIEW (document:read + document:download + certificate:read) or '
    'VERIFY (VIEW''s grants PLUS document:verify). Never implies '
    'document:redact, document:share (resharing), or any write/delete '
    'action — master prompt §7/§25.';
COMMENT ON COLUMN document_shares.expires_at IS
    'NULL means non-expiring. Enforced server-side on every access check '
    '(internal/authz, and this same condition mirrored in RLS below) — '
    'never left to the frontend to hide an expired share.';

-- The hot path every document read/download/verify falls back to once
-- case-relationship ABAC has already failed (see internal/authz/
-- share_policy.go) — and, as a partial UNIQUE index, this simultaneously
-- enforces master prompt §31: at most one ACTIVE share per (document,
-- recipient) pair. A second "share this document with the same person
-- again" attempt while one is already active is therefore rejected by the
-- database itself (surfaced as 409 Conflict — see
-- internal/service.isUniqueViolation), not merely discouraged in
-- application code.
CREATE UNIQUE INDEX document_shares_active_unique
    ON document_shares(document_id, shared_with_user_id)
    WHERE status = 'ACTIVE';

CREATE INDEX idx_document_shares_document_id ON document_shares(document_id);
CREATE INDEX idx_document_shares_shared_with_user_id ON document_shares(shared_with_user_id);
CREATE INDEX idx_document_shares_created_by_user_id ON document_shares(created_by_user_id);
CREATE INDEX idx_document_shares_status ON document_shares(status);
CREATE INDEX idx_document_shares_expires_at ON document_shares(expires_at);

-- =============================================================================
-- 2. ROW-LEVEL SECURITY — document_shares
-- =============================================================================

ALTER TABLE document_shares ENABLE ROW LEVEL SECURITY;
ALTER TABLE document_shares FORCE ROW LEVEL SECURITY;

-- Visible to: ADMIN, the share's creator, the share's recipient, or any
-- other active member of the document's own case (mirrors
-- redactions_select's exact join shape) — a generous SELECT policy
-- because master prompt §9's "only authorized users may see a document's
-- share list" is enforced at the APPLICATION layer
-- (ShareService.ListShares gates on CanAccessDocument(..., document:share)
-- before ever issuing this query), not by narrowing this policy down to
-- the point where a legitimate recipient couldn't even see their OWN
-- incoming share row.
CREATE POLICY document_shares_select ON document_shares FOR SELECT
    USING (
        current_app_role() = 'ADMIN'
        OR created_by_user_id = current_app_user_id()
        OR shared_with_user_id = current_app_user_id()
        OR EXISTS (
            SELECT 1 FROM documents d
            JOIN case_members cm ON cm.case_id = d.case_id
            WHERE d.id = document_shares.document_id
              AND cm.user_id = current_app_user_id()
              AND cm.removed_at IS NULL
        )
    );

CREATE POLICY document_shares_insert ON document_shares FOR INSERT
    WITH CHECK (
        current_app_user_id() IS NOT NULL
        AND created_by_user_id = current_app_user_id()
        AND (
            current_app_role() = 'ADMIN'
            OR EXISTS (
                SELECT 1 FROM documents d
                JOIN case_members cm ON cm.case_id = d.case_id
                WHERE d.id = document_shares.document_id
                  AND cm.user_id = current_app_user_id()
                  AND cm.removed_at IS NULL
            )
        )
    );

-- UPDATE is used for exactly one transition (ACTIVE -> REVOKED via
-- ShareService.RevokeShare) — never to change document_id,
-- shared_with_user_id, or permission after creation. Anyone who could
-- have CREATED a share for this document may also revoke ANY share on
-- it (master prompt §9/§10's "authorized to manage the document's
-- sharing" — not narrowed to "only the original creator"; see
-- docs/SECURITY.md's "Document Sharing" for why).
CREATE POLICY document_shares_update ON document_shares FOR UPDATE
    USING (
        current_app_role() = 'ADMIN'
        OR EXISTS (
            SELECT 1 FROM documents d
            JOIN case_members cm ON cm.case_id = d.case_id
            WHERE d.id = document_shares.document_id
              AND cm.user_id = current_app_user_id()
              AND cm.removed_at IS NULL
        )
    );

-- No DELETE policy or grant: a share is immutable history once created,
-- exactly like redactions/compliance_certificates — see the table
-- comment above.

GRANT SELECT, INSERT, UPDATE ON document_shares TO evidentia_app;
REVOKE DELETE ON document_shares FROM evidentia_app;

-- =============================================================================
-- 3. DELEGATED ACCESS — extend documents/compliance_certificates RLS
-- =============================================================================
--
-- Master prompt §19: RLS must permit access when "user is directly
-- authorized OR user has active valid delegated access" — this is that
-- second path, added as an additional OR-branch to the EXISTING
-- documents_select/compliance_certificates_select policies (never a
-- DROP+recreate that could silently lose behavior — ALTER POLICY changes
-- only the USING clause, everything else about the policy is untouched).
-- The application-layer mirror of this exact condition lives in
-- internal/authz/share_policy.go's shareGrantsAccess, which every
-- document/certificate route already calls via CanAccessDocument — the
-- two layers independently enforce the identical rule, neither trusting
-- the other alone (the same "reinforcing, not redundant" posture
-- documented throughout db/migrations/000001_init_schema.up.sql).
--
-- Deliberately NOT extended: redactions_select (no route exposes
-- redaction lineage to a non-case-member today — nothing to authorize),
-- documents_insert/documents_update (a share never grants write access —
-- master prompt §21/§25).
--
-- has_active_document_share is NOT a plain EXISTS subquery inlined into
-- the two policies below, for a load-bearing reason: document_shares has
-- its OWN RLS (document_shares_select), whose USING clause itself joins
-- back into documents (to let a case member see a document's shares).
-- Inlining a raw "EXISTS (SELECT 1 FROM document_shares ...)" into
-- documents_select would therefore require evaluating
-- document_shares_select for every candidate row, which in turn
-- re-evaluates documents_select — PostgreSQL detects this as infinite
-- recursion ("infinite recursion detected in policy for relation
-- documents", SQLSTATE 42P17) and refuses the query outright. A
-- SECURITY DEFINER function, owned by the migrator role (a superuser —
-- see \du in any environment this runs in — so it bypasses RLS
-- entirely, FORCE ROW LEVEL SECURITY notwithstanding: superusers are
-- always exempt), queries document_shares directly with no RLS
-- re-entry, breaking the cycle. This does not weaken anything: the
-- function's own WHERE clause is the exact same condition
-- document_shares_select and share_policy.go's shareGrantsAccess apply,
-- just evaluated without triggering a second pass through documents'
-- own policy.
CREATE FUNCTION has_active_document_share(p_document_id UUID, p_user_id UUID) RETURNS BOOLEAN
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = public AS $$
        SELECT EXISTS (
            SELECT 1 FROM document_shares ds
            WHERE ds.document_id = p_document_id
              AND ds.shared_with_user_id = p_user_id
              AND ds.status = 'ACTIVE'
              AND (ds.expires_at IS NULL OR ds.expires_at > now())
        );
    $$;

COMMENT ON FUNCTION has_active_document_share(UUID, UUID) IS
    'SECURITY DEFINER solely to break the documents_select <-> '
    'document_shares_select RLS recursion (see the CREATE FUNCTION site''s '
    'comment) — evaluates the identical condition either policy would, '
    'just without re-entering documents'' own RLS. Takes no application '
    'trust on faith: current_app_user_id() is still the caller''s own '
    'transaction-local identity, passed in as p_user_id by the policies '
    'below, never a value this function invents itself.';

GRANT EXECUTE ON FUNCTION has_active_document_share(UUID, UUID) TO evidentia_app;

ALTER POLICY documents_select ON documents USING (
    current_app_role() = 'ADMIN'
    OR EXISTS (
        SELECT 1 FROM case_members cm
        WHERE cm.case_id = documents.case_id
          AND cm.user_id = current_app_user_id()
          AND cm.removed_at IS NULL
    )
    OR has_active_document_share(documents.id, current_app_user_id())
);

ALTER POLICY compliance_certificates_select ON compliance_certificates USING (
    current_app_role() = 'ADMIN'
    OR EXISTS (
        SELECT 1 FROM documents d
        JOIN case_members cm ON cm.case_id = d.case_id
        WHERE d.id = compliance_certificates.document_id
          AND cm.user_id = current_app_user_id()
          AND cm.removed_at IS NULL
    )
    OR has_active_document_share(compliance_certificates.document_id, current_app_user_id())
);
