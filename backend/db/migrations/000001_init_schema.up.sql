-- Evidentia — Initial Schema (Up)
--
-- Design decisions (see docs/DATABASE_SCHEMA.md for full rationale):
--   * UUID primary keys everywhere (gen_random_uuid(), built into Postgres
--     core since v13 — no extension required).
--   * TIMESTAMPTZ for every timestamp, defaulting to now() (UTC).
--   * citext for case-insensitive, unique email comparison.
--   * Controlled-vocabulary columns (status, document_type, ...) use TEXT +
--     CHECK rather than native ENUM, so future values are a plain
--     ALTER TABLE ... DROP/ADD CONSTRAINT instead of the more awkward
--     ALTER TYPE ... ADD VALUE transactional restrictions.
--   * SHA-256 hashes stored as BYTEA (true binary data), constrained to
--     exactly 32 bytes.
--   * Evidence-integrity posture: no ON DELETE CASCADE from business/
--     evidence tables — deleting a user/case/document while it still has
--     dependent rows is blocked (RESTRICT), so nothing needed for the
--     audit trail can silently disappear via a cascade. Pure association
--     (join) tables (user_roles, role_permissions) do cascade, since a
--     dangling link row is meaningless once either side is gone.
--   * No hard-delete columns/paths for users/cases/documents/audit_log/
--     certificates — status/removed_at/archived_at express lifecycle
--     instead (see individual table comments).
--
-- This migration intentionally does NOT implement: authentication logic,
-- RBAC/ABAC business rules, audit-chain hash computation, document
-- hashing/upload, redaction processing, or certificate generation. It
-- establishes the schema, constraints, indexes, RLS, and privileges those
-- later systems will build on.

-- =============================================================================
-- 1. EXTENSIONS
-- =============================================================================

-- Case-insensitive email comparison/uniqueness (see users table below).
-- gen_random_uuid() needs no extension on Postgres 13+ (verified against
-- the postgres:15 image this project targets).
CREATE EXTENSION IF NOT EXISTS citext;

-- =============================================================================
-- 2. ROLES (reference data table — see also "8. DATABASE ROLES" near the
--    end of this file for the separate concept of POSTGRES login roles)
-- =============================================================================

CREATE TABLE roles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    description TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT roles_name_unique UNIQUE (name)
);

COMMENT ON TABLE roles IS
    'Fixed catalog of application roles (ADMIN, POLICE, FORENSICS, LAWYER, '
    'JUDGE, ...). Row-level authorization rules live in application RBAC/ABAC '
    '(later systems); this table is the normalized reference list.';

-- =============================================================================
-- 3. PERMISSIONS (reference data table)
-- =============================================================================

CREATE TABLE permissions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    description TEXT,
    resource    TEXT NOT NULL,
    action      TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT permissions_name_unique UNIQUE (name)
);

COMMENT ON TABLE permissions IS
    'Fine-grained permission catalog (e.g. name=''case:create'', '
    'resource=''case'', action=''create''). Not constrained to a fixed '
    'resource/action vocabulary here — the catalog is expected to grow as '
    'later systems are implemented; that growth is ordinary reference-data '
    'seeding, not a schema migration.';

-- =============================================================================
-- 4. USERS
-- =============================================================================

CREATE TABLE users (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email          CITEXT NOT NULL,
    password_hash  TEXT NOT NULL,
    first_name     TEXT NOT NULL,
    last_name      TEXT NOT NULL,
    display_name   TEXT,
    phone          TEXT,
    status         TEXT NOT NULL DEFAULT 'active',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at  TIMESTAMPTZ,

    CONSTRAINT users_email_unique UNIQUE (email),
    CONSTRAINT users_status_check CHECK (status IN ('active', 'inactive', 'suspended'))
);

COMMENT ON TABLE users IS
    'Authentication/authorization identity. password_hash stores a bcrypt '
    'hash only (System 3 computes it) — never plaintext, never a JWT or '
    'refresh token. Email uniqueness is case-insensitive via citext, so '
    '''User@Example.com'' and ''user@example.com'' collide as intended.';
COMMENT ON COLUMN users.password_hash IS
    'bcrypt hash. Application code must never log this column.';
COMMENT ON COLUMN users.status IS
    'active | inactive | suspended. Deactivation/suspension is a status '
    'change, never a row deletion — see design-decisions header comment.';

-- =============================================================================
-- 5. USER_ROLES (many-to-many)
-- =============================================================================

CREATE TABLE user_roles (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id    UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT user_roles_user_role_unique UNIQUE (user_id, role_id)
);

COMMENT ON TABLE user_roles IS
    'Many-to-many user<->role assignment. Cascades on either side: a '
    'dangling assignment row is meaningless once the user or role itself '
    'is gone — this does NOT cascade-delete the user or role, only this '
    'join row. The schema does not assume "exactly one role per user".';

CREATE INDEX idx_user_roles_user_id ON user_roles(user_id);
CREATE INDEX idx_user_roles_role_id ON user_roles(role_id);

-- =============================================================================
-- 6. ROLE_PERMISSIONS (many-to-many)
-- =============================================================================

CREATE TABLE role_permissions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    role_id       UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_id UUID NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT role_permissions_role_permission_unique UNIQUE (role_id, permission_id)
);

CREATE INDEX idx_role_permissions_role_id ON role_permissions(role_id);
CREATE INDEX idx_role_permissions_permission_id ON role_permissions(permission_id);

-- =============================================================================
-- 7. CASES
-- =============================================================================

CREATE TABLE cases (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    case_number TEXT NOT NULL,
    title       TEXT NOT NULL,
    description TEXT,
    status      TEXT NOT NULL DEFAULT 'OPEN',
    metadata    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_by  UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT cases_case_number_unique UNIQUE (case_number),
    CONSTRAINT cases_status_check CHECK (
        status IN ('OPEN', 'UNDER_INVESTIGATION', 'SUBMITTED', 'UNDER_REVIEW', 'CLOSED', 'ARCHIVED')
    )
);

COMMENT ON TABLE cases IS
    'A case is never hard-deleted (created_by uses ON DELETE RESTRICT, and '
    'no DELETE privilege is granted to the runtime role below) — lifecycle '
    'is expressed entirely through status. No agencies table: agency-scoped '
    'isolation, if needed, can key off case_id directly; see '
    'docs/DATABASE_SCHEMA.md for why this was not added speculatively.';

CREATE INDEX idx_cases_created_by ON cases(created_by);
CREATE INDEX idx_cases_status ON cases(status);
CREATE INDEX idx_cases_created_at ON cases(created_at);

-- =============================================================================
-- 8. CASE_MEMBERS
-- =============================================================================

CREATE TABLE case_members (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id         UUID NOT NULL REFERENCES cases(id) ON DELETE RESTRICT,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    membership_type TEXT NOT NULL,
    added_by        UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    removed_at      TIMESTAMPTZ,

    CONSTRAINT case_members_membership_type_check CHECK (
        membership_type IN ('OWNER', 'INVESTIGATOR', 'FORENSICS', 'LAWYER', 'JUDGE', 'VIEWER')
    )
);

COMMENT ON TABLE case_members IS
    'The foundation of case-level access control: a lawyer (or any role) '
    'may only access a case they are explicitly attached to here. Removal '
    'is a soft delete (removed_at set), never a row deletion, so historical '
    'membership is preserved for audit purposes. At most one ACTIVE '
    '(removed_at IS NULL) membership row per (case_id, user_id) — see the '
    'partial unique index below — but a user may have multiple historical '
    '(removed) rows over time.';

-- Enforces "at most one active membership per (case, user)" while still
-- allowing historical (removed) rows for the same pair to coexist.
CREATE UNIQUE INDEX idx_case_members_active_unique
    ON case_members(case_id, user_id) WHERE removed_at IS NULL;

CREATE INDEX idx_case_members_case_id ON case_members(case_id);
CREATE INDEX idx_case_members_user_id ON case_members(user_id);

-- =============================================================================
-- 9. CASE_INVOLVED_PARTIES
-- =============================================================================

CREATE TABLE case_involved_parties (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id      UUID NOT NULL REFERENCES cases(id) ON DELETE RESTRICT,
    party_type   TEXT NOT NULL,
    display_name TEXT NOT NULL,
    metadata     JSONB NOT NULL DEFAULT '{}'::jsonb,
    added_by     UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT case_involved_parties_party_type_check CHECK (
        party_type IN ('VICTIM', 'WITNESS', 'SUSPECT', 'ACCUSED', 'OTHER')
    )
);

COMMENT ON TABLE case_involved_parties IS
    'Parties connected to a case (victims, witnesses, suspects, ...). '
    'metadata is SENSITIVE (may hold contact details, statements, etc.) and '
    'is protected today only by case-membership-based row visibility (see '
    'RLS below) — column-level redaction for specific roles (e.g. hiding a '
    'witness''s contact info from a subset of case members) is application-'
    'layer ABAC, not yet implemented.';
COMMENT ON COLUMN case_involved_parties.metadata IS
    'SENSITIVE: may contain PII. Do not expose through generic/unfiltered '
    'queries. Store only what the case genuinely requires.';

CREATE INDEX idx_case_involved_parties_case_id ON case_involved_parties(case_id);

-- =============================================================================
-- 10. DOCUMENTS
-- =============================================================================

CREATE TABLE documents (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id             UUID NOT NULL REFERENCES cases(id) ON DELETE RESTRICT,
    parent_document_id  UUID REFERENCES documents(id) ON DELETE RESTRICT,
    document_type       TEXT NOT NULL,
    filename            TEXT NOT NULL,
    description         TEXT,
    mime_type           TEXT NOT NULL,
    file_size           BIGINT NOT NULL,
    sha256_hash         BYTEA NOT NULL,
    storage_bucket      TEXT NOT NULL,
    storage_object_key  TEXT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'ACTIVE',
    metadata            JSONB NOT NULL DEFAULT '{}'::jsonb,
    uploaded_by         UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    uploaded_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT documents_storage_key_unique UNIQUE (storage_bucket, storage_object_key),
    CONSTRAINT documents_file_size_check CHECK (file_size >= 0),
    CONSTRAINT documents_sha256_hash_length_check CHECK (octet_length(sha256_hash) = 32),
    CONSTRAINT documents_document_type_check CHECK (
        document_type IN ('FIR', 'FORENSIC_REPORT', 'PHOTO_EVIDENCE', 'WITNESS_STATEMENT', 'OTHER')
    ),
    CONSTRAINT documents_status_check CHECK (status IN ('ACTIVE', 'ARCHIVED', 'TAMPERED'))
);

COMMENT ON TABLE documents IS
    'Metadata + integrity reference only — raw bytes live in MinIO '
    '(storage_bucket/storage_object_key), never in Postgres. A redaction '
    'produces a NEW row here (parent_document_id pointing at the source); '
    'the original is never overwritten (see redactions table). There is no '
    '''DELETED'' status: evidence is archived (ARCHIVED), never removed at '
    'the schema level — see design-decisions header comment. ''TAMPERED'' '
    'is reserved for a future integrity-verification system (System 7) to '
    'mark a document whose stored hash no longer matches its bytes; this '
    'migration only reserves the value.';
COMMENT ON COLUMN documents.sha256_hash IS
    'Raw 32-byte SHA-256 digest (BYTEA, not hex text) computed by the '
    'application (System 7) at upload time. Hex-encode only at the API/'
    'JSON boundary.';

CREATE INDEX idx_documents_case_id ON documents(case_id);
CREATE INDEX idx_documents_uploaded_by ON documents(uploaded_by);
CREATE INDEX idx_documents_document_type ON documents(document_type);
CREATE INDEX idx_documents_parent_document_id ON documents(parent_document_id);
CREATE INDEX idx_documents_sha256_hash ON documents(sha256_hash);

-- =============================================================================
-- 11. REDACTIONS
-- =============================================================================

CREATE TABLE redactions (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_document_id  UUID NOT NULL REFERENCES documents(id) ON DELETE RESTRICT,
    result_document_id  UUID NOT NULL REFERENCES documents(id) ON DELETE RESTRICT,
    region_data         JSONB NOT NULL,
    reason              TEXT,
    created_by          UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT redactions_result_document_unique UNIQUE (result_document_id),
    CONSTRAINT redactions_source_ne_result_check CHECK (source_document_id <> result_document_id)
);

COMMENT ON TABLE redactions IS
    'Links a source document to the derivative (redacted) document '
    'produced from it. result_document_id is UNIQUE: a given document row '
    'is the output of at most one redaction operation. region_data is '
    'JSONB (coordinates/pages) since the exact shape is expected to evolve '
    'with the redaction UI; System 2 does not fix its structure. No '
    'UPDATE/DELETE privilege is granted below — once created, a redaction '
    'record is immutable history.';

CREATE INDEX idx_redactions_source_document_id ON redactions(source_document_id);
CREATE INDEX idx_redactions_result_document_id ON redactions(result_document_id);

-- =============================================================================
-- 12. AUDIT_LOG
-- =============================================================================

CREATE TABLE audit_log (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seq           BIGINT GENERATED ALWAYS AS IDENTITY,
    "timestamp"   TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id       UUID REFERENCES users(id) ON DELETE RESTRICT,
    role          TEXT,
    action        TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id   UUID,
    case_id       UUID REFERENCES cases(id) ON DELETE RESTRICT,
    metadata      JSONB NOT NULL DEFAULT '{}'::jsonb,
    prev_hash     BYTEA,
    hash          BYTEA NOT NULL,

    CONSTRAINT audit_log_seq_unique UNIQUE (seq),
    CONSTRAINT audit_log_hash_unique UNIQUE (hash),
    CONSTRAINT audit_log_hash_length_check CHECK (octet_length(hash) = 32),
    CONSTRAINT audit_log_prev_hash_length_check CHECK (prev_hash IS NULL OR octet_length(prev_hash) = 32)
);

COMMENT ON TABLE audit_log IS
    'Append-only, hash-chained security audit trail. id (UUID) is the '
    'external identifier; seq (an identity column) is the deterministic, '
    'gap-free monotonic ordering used for chain traversal — timestamps '
    'alone are not trusted for ordering (clock skew, concurrent inserts). '
    'user_id/case_id are nullable (a system-initiated action, or an action '
    'with no case context, e.g. login, still gets an entry). resource_id '
    'is intentionally NOT a foreign key: resource_type varies across '
    'multiple tables (documents, cases, users, ...) and Postgres cannot '
    'express a polymorphic FK — referential integrity for it is an '
    'application-layer concern. This table''s runtime privileges grant '
    'SELECT and INSERT only (see grants below): no UPDATE, no DELETE, at '
    'the database level, not just in application code. Hash chain '
    'computation (hash/prev_hash values) belongs to System 8 — this '
    'migration only establishes storage, constraints, and the invariant '
    'that at most one row may claim a given predecessor (see the partial '
    'unique indexes below).';
COMMENT ON COLUMN audit_log.role IS
    'The role the actor was acting as at write time, captured verbatim — '
    'not re-derived from user_roles later, since a user''s roles can '
    'change after the fact but the audit record must reflect what was '
    'true when the action occurred.';
COMMENT ON COLUMN audit_log.prev_hash IS
    'NULL only for the single genesis entry (see '
    'idx_audit_log_single_genesis below, which enforces that "only one" '
    'at the database level).';

-- "One canonical predecessor -> one new entry": no two rows may claim the
-- same non-null prev_hash.
CREATE UNIQUE INDEX idx_audit_log_prev_hash_unique
    ON audit_log(prev_hash) WHERE prev_hash IS NOT NULL;

-- At most one row total may have a NULL prev_hash (the genesis entry) —
-- a unique index on a constant expression, filtered to the rows in
-- question, is the standard Postgres idiom for "at most one" instead of
-- "one per distinct value".
CREATE UNIQUE INDEX idx_audit_log_single_genesis
    ON audit_log((1)) WHERE prev_hash IS NULL;

CREATE INDEX idx_audit_log_timestamp ON audit_log("timestamp");
CREATE INDEX idx_audit_log_user_id ON audit_log(user_id);
CREATE INDEX idx_audit_log_case_id ON audit_log(case_id);
CREATE INDEX idx_audit_log_action ON audit_log(action);
CREATE INDEX idx_audit_log_resource ON audit_log(resource_type, resource_id);

-- =============================================================================
-- 13. COMPLIANCE_CERTIFICATES
-- =============================================================================

CREATE TABLE compliance_certificates (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id          UUID NOT NULL REFERENCES documents(id) ON DELETE RESTRICT,
    document_hash        BYTEA NOT NULL,
    certificate_version  TEXT NOT NULL,
    certificate_data      JSONB NOT NULL DEFAULT '{}'::jsonb,
    generated_by         UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    generated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT compliance_certificates_document_hash_length_check CHECK (octet_length(document_hash) = 32)
);

COMMENT ON TABLE compliance_certificates IS
    'A certificate is bound to the exact document hash/version it '
    'represents at generation time (document_hash is stored redundantly '
    'alongside document_id) so it remains historically meaningful even if '
    'the referenced document''s current metadata changes later — a '
    'redacted derivative gets its own certificate row, tied to its own '
    'hash, distinct from the original''s. No UPDATE/DELETE privilege is '
    'granted below: once generated, a certificate is immutable history.';

CREATE INDEX idx_compliance_certificates_document_id ON compliance_certificates(document_id);

-- =============================================================================
-- 14. ROW-LEVEL SECURITY — HELPER FUNCTIONS
-- =============================================================================

-- Reads the transaction-local application identity the Go layer sets via
-- set_config('app.user_id', <uuid>, true) at the start of each request-
-- scoped transaction (true = transaction-local, never leaks across pooled
-- connections/transactions). Returns NULL — never an error — when unset,
-- so ordinary operations without an application identity (e.g. a plain
-- health check query) do not fail; RLS policies below treat NULL as "no
-- identity", which denies access to protected rows (fail closed).
CREATE FUNCTION current_app_user_id() RETURNS UUID
    LANGUAGE sql STABLE AS $$
        SELECT NULLIF(current_setting('app.user_id', true), '')::uuid;
    $$;

-- Same contract as current_app_user_id(), for the acting role name the Go
-- layer sets via set_config('app.role', <role>, true).
CREATE FUNCTION current_app_role() RETURNS TEXT
    LANGUAGE sql STABLE AS $$
        SELECT NULLIF(current_setting('app.role', true), '');
    $$;

COMMENT ON FUNCTION current_app_user_id() IS
    'Transaction-local application identity (see set_config(''app.user_id'', '
    '..., true)). NULL when absent — RLS policies must treat that as "deny", '
    'never as "unrestricted". Not SECURITY DEFINER: it only reads a '
    'session/transaction setting, nothing that requires elevated privilege.';
COMMENT ON FUNCTION current_app_role() IS
    'Transaction-local application role (see set_config(''app.role'', ..., '
    'true)). Trusted input: the application is responsible for setting '
    'this only after its own authentication/authorization has determined '
    'the caller''s role. RLS here is defense-in-depth against a buggy '
    'query, not a substitute for correct application-layer RBAC/ABAC.';

-- =============================================================================
-- 15. ROW-LEVEL SECURITY — POLICIES
-- =============================================================================
--
-- Fundamental row isolation only: "you must be an active member of a case
-- (case_members, removed_at IS NULL) to see anything scoped to it, or be
-- ADMIN". Role-specific business rules (police jurisdiction scope, judge
-- docket assignment, ...) are NOT hardcoded here — they are application-
-- layer ABAC (later systems) that decides which case_members rows to
-- create/query in the first place. RLS is the backstop that keeps a bug in
-- that application logic from leaking cross-case data, not a replacement
-- for it (see master prompt "defense in depth").
--
-- users/roles/permissions/user_roles/role_permissions intentionally do NOT
-- get RLS: there is no per-row ownership rule for this reference/identity
-- data at this system's scope, and enabling RLS without a real rule to
-- express is exactly the "confusing behavior" the master prompt warns
-- against. They are protected by table-level grants instead (below).

-- ---- cases ----

ALTER TABLE cases ENABLE ROW LEVEL SECURITY;
ALTER TABLE cases FORCE ROW LEVEL SECURITY;

-- created_by = current_app_user_id() is checked directly (not via a
-- case_members subquery) so a case's creator can see it immediately after
-- creation, before any case_members row exists for it — otherwise
-- creating the case and then inserting its first (owner) case_members row
-- would deadlock: that insert's own policy needs to confirm the case is
-- visible to its creator, which would otherwise require a case_members
-- row that doesn't exist yet. Empirically verified while developing this
-- migration.
CREATE POLICY cases_select ON cases FOR SELECT
    USING (
        current_app_role() = 'ADMIN'
        OR created_by = current_app_user_id()
        OR EXISTS (
            SELECT 1 FROM case_members cm
            WHERE cm.case_id = cases.id
              AND cm.user_id = current_app_user_id()
              AND cm.removed_at IS NULL
        )
    );

CREATE POLICY cases_insert ON cases FOR INSERT
    WITH CHECK (
        current_app_user_id() IS NOT NULL
        AND created_by = current_app_user_id()
    );

CREATE POLICY cases_update ON cases FOR UPDATE
    USING (
        current_app_role() = 'ADMIN'
        OR created_by = current_app_user_id()
        OR EXISTS (
            SELECT 1 FROM case_members cm
            WHERE cm.case_id = cases.id
              AND cm.user_id = current_app_user_id()
              AND cm.removed_at IS NULL
        )
    );

-- No DELETE policy: combined with no DELETE grant below, cases can never
-- be removed through the runtime application role.

-- ---- case_members ----
--
-- IMPORTANT: policies on this table must never re-query case_members
-- itself (directly or via a subquery that, in turn, queries it again) —
-- Postgres detects that as "infinite recursion detected in policy for
-- relation case_members" and errors the query outright, empirically
-- verified while developing this migration. A policy here may check the
-- row's OWN columns directly, or query OTHER tables (e.g. cases), but not
-- case_members. As a consequence, a member can see their own membership
-- row but not (via this table alone) their co-members' rows — listing a
-- case's full team is deferred to a later system, which can serve it via
-- a narrowly scoped mechanism if needed.

ALTER TABLE case_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE case_members FORCE ROW LEVEL SECURITY;

CREATE POLICY case_members_select ON case_members FOR SELECT
    USING (
        current_app_role() = 'ADMIN'
        OR user_id = current_app_user_id()
    );

-- Membership is added/removed by the case's creator (a proxy for "owner"
-- that lives on cases, not case_members, precisely to avoid the
-- self-reference above) or an admin.
CREATE POLICY case_members_insert ON case_members FOR INSERT
    WITH CHECK (
        current_app_user_id() IS NOT NULL
        AND added_by = current_app_user_id()
        AND (
            current_app_role() = 'ADMIN'
            OR EXISTS (
                SELECT 1 FROM cases c
                WHERE c.id = case_members.case_id
                  AND c.created_by = current_app_user_id()
            )
        )
    );

CREATE POLICY case_members_update ON case_members FOR UPDATE
    USING (
        current_app_role() = 'ADMIN'
        OR EXISTS (
            SELECT 1 FROM cases c
            WHERE c.id = case_members.case_id
              AND c.created_by = current_app_user_id()
        )
    );

-- ---- case_involved_parties ----

ALTER TABLE case_involved_parties ENABLE ROW LEVEL SECURITY;
ALTER TABLE case_involved_parties FORCE ROW LEVEL SECURITY;

CREATE POLICY case_involved_parties_select ON case_involved_parties FOR SELECT
    USING (
        current_app_role() = 'ADMIN'
        OR EXISTS (
            SELECT 1 FROM case_members cm
            WHERE cm.case_id = case_involved_parties.case_id
              AND cm.user_id = current_app_user_id()
              AND cm.removed_at IS NULL
        )
    );

CREATE POLICY case_involved_parties_insert ON case_involved_parties FOR INSERT
    WITH CHECK (
        current_app_role() = 'ADMIN'
        OR EXISTS (
            SELECT 1 FROM case_members cm
            WHERE cm.case_id = case_involved_parties.case_id
              AND cm.user_id = current_app_user_id()
              AND cm.removed_at IS NULL
        )
    );

CREATE POLICY case_involved_parties_update ON case_involved_parties FOR UPDATE
    USING (
        current_app_role() = 'ADMIN'
        OR EXISTS (
            SELECT 1 FROM case_members cm
            WHERE cm.case_id = case_involved_parties.case_id
              AND cm.user_id = current_app_user_id()
              AND cm.removed_at IS NULL
        )
    );

-- ---- documents ----

ALTER TABLE documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE documents FORCE ROW LEVEL SECURITY;

CREATE POLICY documents_select ON documents FOR SELECT
    USING (
        current_app_role() = 'ADMIN'
        OR EXISTS (
            SELECT 1 FROM case_members cm
            WHERE cm.case_id = documents.case_id
              AND cm.user_id = current_app_user_id()
              AND cm.removed_at IS NULL
        )
    );

CREATE POLICY documents_insert ON documents FOR INSERT
    WITH CHECK (
        current_app_user_id() IS NOT NULL
        AND uploaded_by = current_app_user_id()
        AND (
            current_app_role() = 'ADMIN'
            OR EXISTS (
                SELECT 1 FROM case_members cm
                WHERE cm.case_id = documents.case_id
                  AND cm.user_id = current_app_user_id()
                  AND cm.removed_at IS NULL
            )
        )
    );

CREATE POLICY documents_update ON documents FOR UPDATE
    USING (
        current_app_role() = 'ADMIN'
        OR EXISTS (
            SELECT 1 FROM case_members cm
            WHERE cm.case_id = documents.case_id
              AND cm.user_id = current_app_user_id()
              AND cm.removed_at IS NULL
        )
    );

-- No DELETE policy or grant: evidence documents are archived, never deleted.

-- ---- redactions ----

ALTER TABLE redactions ENABLE ROW LEVEL SECURITY;
ALTER TABLE redactions FORCE ROW LEVEL SECURITY;

CREATE POLICY redactions_select ON redactions FOR SELECT
    USING (
        current_app_role() = 'ADMIN'
        OR EXISTS (
            SELECT 1 FROM documents d
            JOIN case_members cm ON cm.case_id = d.case_id
            WHERE d.id = redactions.source_document_id
              AND cm.user_id = current_app_user_id()
              AND cm.removed_at IS NULL
        )
    );

CREATE POLICY redactions_insert ON redactions FOR INSERT
    WITH CHECK (
        current_app_user_id() IS NOT NULL
        AND created_by = current_app_user_id()
        AND (
            current_app_role() = 'ADMIN'
            OR EXISTS (
                SELECT 1 FROM documents d
                JOIN case_members cm ON cm.case_id = d.case_id
                WHERE d.id = redactions.source_document_id
                  AND cm.user_id = current_app_user_id()
                  AND cm.removed_at IS NULL
            )
        )
    );

-- No UPDATE/DELETE policy or grant: redactions are immutable once created.

-- ---- compliance_certificates ----

ALTER TABLE compliance_certificates ENABLE ROW LEVEL SECURITY;
ALTER TABLE compliance_certificates FORCE ROW LEVEL SECURITY;

CREATE POLICY compliance_certificates_select ON compliance_certificates FOR SELECT
    USING (
        current_app_role() = 'ADMIN'
        OR EXISTS (
            SELECT 1 FROM documents d
            JOIN case_members cm ON cm.case_id = d.case_id
            WHERE d.id = compliance_certificates.document_id
              AND cm.user_id = current_app_user_id()
              AND cm.removed_at IS NULL
        )
    );

CREATE POLICY compliance_certificates_insert ON compliance_certificates FOR INSERT
    WITH CHECK (
        current_app_user_id() IS NOT NULL
        AND generated_by = current_app_user_id()
        AND (
            current_app_role() = 'ADMIN'
            OR EXISTS (
                SELECT 1 FROM documents d
                JOIN case_members cm ON cm.case_id = d.case_id
                WHERE d.id = compliance_certificates.document_id
                  AND cm.user_id = current_app_user_id()
                  AND cm.removed_at IS NULL
            )
        )
    );

-- No UPDATE/DELETE policy or grant: certificates are immutable once generated.

-- ---- audit_log ----

ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_log FORCE ROW LEVEL SECURITY;

CREATE POLICY audit_log_select ON audit_log FOR SELECT
    USING (
        current_app_role() = 'ADMIN'
        OR user_id = current_app_user_id()
        OR (
            case_id IS NOT NULL
            AND EXISTS (
                SELECT 1 FROM case_members cm
                WHERE cm.case_id = audit_log.case_id
                  AND cm.user_id = current_app_user_id()
                  AND cm.removed_at IS NULL
            )
        )
    );

CREATE POLICY audit_log_insert ON audit_log FOR INSERT
    WITH CHECK (current_app_user_id() IS NOT NULL);

-- No UPDATE/DELETE policy. Combined with the grants below (SELECT, INSERT
-- only — explicitly no UPDATE/DELETE), audit_log is append-only enforced
-- at the database level, not just by application code.

-- =============================================================================
-- 16. DATABASE ROLES
-- =============================================================================
--
-- Two distinct Postgres login roles:
--   * The role that runs this migration (whatever DATABASE_MIGRATOR_USER is
--     configured to — typically the Postgres bootstrap superuser in local
--     development) OWNS every object created above and may freely alter
--     the schema.
--   * evidentia_app, created below, is what the running Go server connects
--     as. It is NOT a superuser, NOT the owner of any table, has
--     NOBYPASSRLS (RLS policies above always apply to it), and receives
--     only the explicit grants listed here — most importantly, no UPDATE
--     or DELETE on audit_log.
--
-- 'changeme_example' matches the same placeholder convention used
-- throughout this repository's docker-compose.yml/.env.example (see
-- docs/DEPLOYMENT.md) — it is a documented local-development default, not
-- a production credential. Any non-throwaway environment MUST rotate it:
--   ALTER ROLE evidentia_app WITH PASSWORD '<strong-random-value>';
-- run as an operational step outside version control (this migration
-- cannot template a real secret into a checked-in, reproducible file).

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname = 'evidentia_app') THEN
        CREATE ROLE evidentia_app LOGIN PASSWORD 'changeme_example'
            NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS NOINHERIT;
    END IF;
END
$$;

DO $$
BEGIN
    EXECUTE format('GRANT CONNECT ON DATABASE %I TO evidentia_app', current_database());
END
$$;

GRANT USAGE ON SCHEMA public TO evidentia_app;

-- Reference/identity data: read for all, write only where the runtime
-- application legitimately manages it (registration/profile updates,
-- role assignment). The permission/role catalogs themselves are
-- migration/seed-managed, not runtime-mutable.
GRANT SELECT ON roles, permissions TO evidentia_app;
GRANT SELECT, INSERT, UPDATE ON users TO evidentia_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON user_roles TO evidentia_app;
GRANT SELECT ON role_permissions TO evidentia_app;

-- Case/document domain: standard read/write, no hard deletes.
GRANT SELECT, INSERT, UPDATE ON cases TO evidentia_app;
GRANT SELECT, INSERT, UPDATE ON case_members TO evidentia_app;
GRANT SELECT, INSERT, UPDATE ON case_involved_parties TO evidentia_app;
GRANT SELECT, INSERT, UPDATE ON documents TO evidentia_app;

-- Immutable-once-created records: insert (and read) only.
GRANT SELECT, INSERT ON redactions TO evidentia_app;
GRANT SELECT, INSERT ON compliance_certificates TO evidentia_app;

-- audit_log: SELECT + INSERT only. This is the hard security requirement
-- from master prompt §26/42 — verified by an integration test
-- (backend/tests/audit_privileges_test.go), not just asserted here.
GRANT SELECT, INSERT ON audit_log TO evidentia_app;

-- Explicit, redundant-but-documented denial: the GRANTs above never
-- included UPDATE/DELETE on audit_log or DELETE on the evidence tables,
-- so these REVOKEs are a no-op in practice — they exist so the intent is
-- unmistakable to a future reader (or a future migration) rather than
-- relying solely on the absence of a GRANT.
REVOKE UPDATE, DELETE ON audit_log FROM evidentia_app;
REVOKE DELETE ON cases, documents, case_members, case_involved_parties, redactions, compliance_certificates FROM evidentia_app;
