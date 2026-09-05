# API Endpoints

## Purpose

TODO: Document the full REST API surface for Evidentia. Cases, Case
Documents (upload/download/verify/certificate/redact/share/shared-with-me),
Authentication, Admin (user management), Audit, and Health/Readiness are
implemented today.

## Authentication (implemented)

All under `/api/v1/auth`. Machine-readable spec:
`backend/docs/swagger/swagger.json` (generate via `make swagger`; not
committed — see `backend/docs/swagger/README.md`). Full design rationale:
[SECURITY.md](./SECURITY.md).

```http
POST /api/v1/auth/login
```

Public — no Authorization header required.

```json
{"email": "user@example.com", "password": "at-least-8-characters"}
```

```json
{
  "success": true,
  "data": {
    "access_token": "...",
    "refresh_token": "...",
    "token_type": "Bearer",
    "expires_in": 900,
    "user": {"id": "...", "email": "...", "first_name": "...", "last_name": "...", "role": "LAWYER"}
  }
}
```

- `400` — malformed body (missing/invalid email, password under 8 characters)
- `401` — `{"success":false,"error":{"code":"UNAUTHORIZED","message":"Invalid email or password","request_id":"..."}}`
  for **any** of: unknown email, wrong password, inactive/suspended account
  — deliberately identical in every case, to avoid revealing which one
  occurred (user enumeration).

```http
POST /api/v1/auth/refresh
```

Public. Rotates the presented refresh token: it is revoked and a new
access + refresh token pair (same session family) is issued.

```json
{"refresh_token": "..."}
```

Same success shape as login. `401` (same generic body as login, message
`"Invalid or expired refresh token"`) for: unknown/malformed token,
already-rotated (reused) token — which also revokes the entire token
family — expired token, or an inactive account.

```http
POST /api/v1/auth/logout
```

**Requires** `Authorization: Bearer <access_token>` (this is the one
exception to "auth routes are public" — see SECURITY.md for why). Revokes
the session identified by the supplied refresh token, if any.

```json
{"refresh_token": "..."}
```

`refresh_token` is optional — omitting it (or passing one already invalid)
still returns `200`; there is nothing else to invalidate (access tokens
are stateless). `401` if the Authorization header is missing/invalid/
expired, or the account has been deactivated since the token was issued.

```json
{"success": true, "data": null}
```

## Cases (implemented — System 5; `/events` — System 13)

```text
POST   /api/v1/cases
GET    /api/v1/cases
GET    /api/v1/cases/:id
PUT    /api/v1/cases/:id
GET    /api/v1/cases/:id/events   (SSE — System 13)
```

All five require `Authorization: Bearer <access_token>`. Required
authorization, per System 4 (`docs/SECURITY.md`'s Authorization section),
wired via `middleware.RequirePermission`/`RequireCaseAccess`:

| Route | Permission (RBAC) | Resource check (ABAC) |
|---|---|---|
| `POST /cases` | `case:create` | — (no resource yet; the creator becomes `cases.created_by`, never a client-supplied value) |
| `GET /cases` | `case:read` | list is scoped by PostgreSQL RLS (the caller's own case relationships), not a per-item check here |
| `GET /cases/:id` | `case:read` | `CanAccessCase` — caller must be ADMIN, the case's creator, or an active `case_members` row |
| `PUT /cases/:id` | `case:update` | `CanAccessCase` (same relationship check) |
| `GET /cases/:id/events` | `case:read` | `CanAccessCase` (the identical check `GET /cases/:id` requires, re-run on every new SSE connection) |

### `GET /cases/:id/events` (SSE — System 13)

`text/event-stream` of this case's real-time notifications —
`DOCUMENT_VERIFICATION_COMPLETED`, `CERTIFICATE_GENERATION_COMPLETED`,
`DOCUMENT_REDACTION_COMPLETED`, `SHARE_CREATED`, `SHARE_REVOKED` (see
`docs/REALTIME_EVENTS.md`'s "Event Catalog"). Sends no initial snapshot —
an event here is a "this case's state may have changed" signal only; the
client's existing `GET /cases/:id` (and related) queries remain the
source of current state. Never closes on its own aside from a periodic
forced reconnect (at most hourly) that re-validates authorization — it
ends only on client disconnect or server shutdown. Same authentication as
any other route (bearer header, no token in the URL). `429` if the
caller already holds too many concurrent SSE connections.

`internal/service.CaseService` independently re-checks the same
authorization internally (not just the HTTP middleware) — see
`ARCHITECTURE.md`'s System 5 section.

### `POST /cases`

```json
{"case_number": "FIR-2026-001", "title": "Theft investigation", "description": "...", "status": "OPEN", "metadata": {}}
```

`status`/`description`/`metadata` are all optional (`status` defaults to
`OPEN`). `id`/`created_by`/`created_at`/`updated_at` are server-controlled
— there is no request field for any of them, so a client cannot supply
one even accidentally. On success (`201`), the response is the same
shape `GET /cases/:id` returns (see below). The creator is also added as
an `OWNER` `case_members` row in the same transaction.

- `400` — missing/invalid `case_number`/`title`, invalid `status`, or
  malformed/oversized `metadata` (see `internal/utils.ValidateJSONMetadata`
  — max 32KB, must be a JSON object).
- `409` — `case_number` already exists (`cases_case_number_unique`).

### `GET /cases`

Query parameters (all optional): `status`, `case_number` (substring
match), `title` (substring match), `created_by` (UUID), `created_from`/
`created_to` (RFC3339 timestamps), `page` (default 1), `page_size`
(default 20, max 100 — larger values are silently clamped, not rejected).
Filtering and pagination both happen in SQL
(`db/queries/cases.sql`'s `ListCasesFiltered`/`CountCasesFiltered`), on
top of whatever PostgreSQL RLS already narrowed the row set to — never an
unfiltered `SELECT *` followed by in-memory filtering.

```json
{
  "success": true,
  "data": {
    "cases": [{"id": "...", "case_number": "...", "title": "...", "status": "OPEN", "created_by": "...", "created_at": "...", "updated_at": "..."}],
    "meta": {"page": 1, "page_size": 20, "total": 1, "total_pages": 1}
  }
}
```

### `GET /cases/:id`

```json
{
  "success": true,
  "data": {
    "id": "...", "case_number": "...", "title": "...", "description": "...",
    "status": "OPEN", "metadata": {}, "created_by": "...", "created_at": "...", "updated_at": "...",
    "involved_parties": [{"id": "...", "party_type": "WITNESS", "display_name": "[REDACTED]", "metadata": {}, "created_at": "..."}],
    "documents": [{"id": "...", "document_type": "OTHER", "filename": "...", "mime_type": "...", "file_size": 0, "status": "ACTIVE", "uploaded_by": "...", "uploaded_at": "..."}],
    "timeline": [{"type": "CASE_CREATED", "timestamp": "...", "summary": "...", "related_id": "..."}],
    "relationship": {"is_owner": true, "is_member": true, "membership_type": "OWNER"}
  }
}
```

`involved_parties[].display_name`/`metadata` are redacted to `"[REDACTED]"`/
`{}` for a `WITNESS`-type party when the caller is not JUDGE/POLICE/ADMIN
— see `internal/authz.SanitizeInvolvedParty`. `documents` is metadata/
references only (never file bytes; capped at 50 rows — full paginated
document listing is System 6's scope). `timeline` is synthesized from
case/document/involved-party timestamps, not read from `audit_log` (see
`ARCHITECTURE.md`'s System 5 section for why).

- `403` — no relationship to this case, OR the case doesn't exist, OR the
  `:id` isn't a valid UUID. All three produce the byte-identical response
  (same status, same generic message) — never a `404` that would confirm
  a specific case ID's existence to an unauthorized caller.

### `PUT /cases/:id`

Full replacement — `title`, `status`, and `metadata` are always required
in the body (`description` optional); this mirrors the existing
`UpdateCase` SQL query, which was already an unconditional 4-column
`UPDATE`, not a partial-patch contract.

```json
{"title": "Updated title", "description": "...", "status": "UNDER_INVESTIGATION", "metadata": {}}
```

Response: same shape as `GET /cases/:id`. `id`/`created_by`/`created_at`
cannot be changed — the request body has no field for any of them.

- `400` — invalid `status` value, OR a status transition not in
  `CaseService`'s documented transition map (`OPEN` → `UNDER_INVESTIGATION`
  → `SUBMITTED` → `UNDER_REVIEW` → `CLOSED` → `ARCHIVED`, with
  `SUBMITTED`/`UNDER_REVIEW` allowed to fall back one step and `ARCHIVED`
  reachable directly from any non-terminal status). This is System 5's own
  conservative starting model — System 2's schema only constrains the
  value set, not a transition graph.
- `403` — same anti-enumeration behavior as `GET /cases/:id`.

### Audit integration

Every successful `POST`/`PUT` records an event via `internal/audit.Recorder`
(the same interface System 3/4 already use — operational log today, System
8's durable hash-chained writer later, no change required here):
`CASE_CREATED` on create; `CASE_UPDATED` on every successful update, plus a
separate `CASE_STATUS_CHANGED` event when `status` actually changed. A
failed/rejected mutation (validation error, authorization denial, invalid
transition, duplicate `case_number`) never produces one of these — see
`case_service_integration_test.go`'s `TestCaseService_*_Denied`/
`*Rejected`/`*Conflict` tests, which assert exactly that.

## Case Documents (implemented — System 6)

```text
POST /api/v1/cases/:id/documents
```

Requires `Authorization: Bearer <access_token>` plus `document:upload`
(RBAC) and `CanAccessCase` on the `:id` case (ABAC) — POLICE/FORENSICS/
ADMIN by seed data; a caller must have a relationship to the case (owner,
active `case_members` row, or ADMIN) before uploading into it. Holding
`document:upload` alone (e.g. as POLICE) never grants access to another
officer's unrelated case.

Content type `multipart/form-data`, streamed (never buffered whole into
memory or a temp file — see `internal/handlers/document/upload.go`).
Fields:

| Field | Required | Notes |
|---|---|---|
| `document_type` | yes | One of `FIR`, `FORENSIC_REPORT`, `PHOTO_EVIDENCE`, `WITNESS_STATEMENT`, `OTHER` — must appear **before** `file` in the multipart body (see below) |
| `description` | no | Free text, max 10,000 bytes, must be valid UTF-8 |
| `file` | yes | The evidence file — must appear **after** `document_type` |

**Field order matters**: the body is read as a true byte stream
(`http.Request.MultipartReader`), which can only be consumed once, in the
order the client sent it. `document_type` (and `description`, if present)
must precede `file` — exactly how a browser's `FormData`/JS multipart
encoder serializes fields appended in that order. `id`/`created_by`/
`uploaded_by`/`bucket`/`object_key`/`sha256_hash` are never accepted as
form fields — there is no field name for any of them; the server always
determines the uploader from the authenticated caller, generates the
document ID and object key itself, and computes the hash from the actual
uploaded bytes.

On success (`201`), the response is a document metadata object — the same
shape embedded in `GET /cases/:id`'s `documents` array (see the Cases
section above):

```json
{
  "success": true,
  "data": {
    "id": "...", "case_id": "...", "document_type": "FIR",
    "filename": "fir-scan.pdf", "description": "...",
    "mime_type": "application/pdf", "file_size": 245098,
    "sha256_hash": "64-hex-chars...", "status": "ACTIVE",
    "uploaded_by": "...", "uploaded_at": "..."
  }
}
```

- `400` — missing `file`, `document_type` absent/invalid, `document_type`
  arrived after `file` in the multipart body, or `description` too
  long/not valid UTF-8.
- `403` — no relationship to the case, OR the case doesn't exist, OR the
  `:id` isn't a valid UUID — identical response in all three cases (no
  confirmation of case existence to an unrelated caller).
- `413` — the file (or the request body as a whole) exceeds
  `MAX_UPLOAD_SIZE`. Both the coarse, whole-request body-size guard
  (`middleware.BodyLimit`) and `DocumentService`'s fine-grained,
  file-content-only streaming guard map to this same status/code — a
  client never sees a different response depending on which layer caught
  it.

## Documents

```text
GET  /api/v1/documents/:id/download
GET  /documents/:id                       (not implemented — see below)
POST /api/v1/documents/:id/verify         (implemented — System 7)
POST /api/v1/documents/:id/redact         (implemented)
POST /api/v1/documents/:id/share          (implemented)
GET  /api/v1/documents/:id/shares         (implemented)
POST /api/v1/documents/:id/shares/:shareId/revoke (implemented)
GET  /api/v1/shared/documents             (implemented)
GET  /api/v1/users/search                 (implemented)
GET  /api/v1/documents/:id/certificate    (implemented — System 7)
```

### `GET /api/v1/documents/:id/download` (implemented — System 6)

Requires `Authorization: Bearer <access_token>` plus `document:download`
(RBAC) and `CanAccessDocument` (ABAC) — a document has no independent
access grant; it inherits its authorization scope entirely from the
caller's relationship to the document's case (owner, active
`case_members` row, or ADMIN), resolved server-side — a client-supplied
case ID is never trusted as proof of access.

Streams the object directly from MinIO to the HTTP response (never
buffered whole in this process). Always served with
`Content-Disposition: attachment` (never rendered inline) and
`X-Content-Type-Options: nosniff`, so a browser never executes/renders
evidence content by default. `Content-Type` is the MIME type detected
server-side from the file's actual bytes at upload time (never the
client-declared `Content-Type` from the original upload request).

- `403` — no relationship to the document's case, OR the document doesn't
  exist, OR the `:id` isn't a valid UUID — identical response in all
  three cases (the same anti-enumeration posture as case detail).
- `503` — the database row exists but the object could not be retrieved
  from storage (e.g. deleted out-of-band, storage outage) — logged
  operationally with the object key for investigation; never a raw MinIO/
  driver error returned to the client.

Every successful download records a `DOCUMENT_DOWNLOADED` audit event
(see "Audit" in `docs/SECURITY.md`) as soon as the object stream is
confirmed available — it does not wait for the client to finish reading
the response body.

### `POST /api/v1/documents/:id/verify` (implemented — System 7)

Requires `Authorization: Bearer <access_token>` plus `document:verify`
(RBAC) and `CanAccessDocument` (ABAC) — the same document-scoped
authorization pattern as download, re-checked independently at the
service layer (`DocumentService.VerifyDocument`), not just by the HTTP
middleware. No request body.

Recomputes the document's SHA-256 hash by streaming the actual object
retrieved from MinIO through the same `pkg/hash` streaming SHA-256 System
6 uses at upload, and compares it against `documents.sha256_hash` — the
canonical hash recorded in PostgreSQL at ingestion time. **The client
never supplies a hash to compare against**: trusting a client-provided
hash as evidence of integrity would defeat the entire point of this
endpoint.

```json
{
  "success": true,
  "data": {
    "document_id": "...",
    "status": "VERIFIED",
    "stored_hash": "64-hex-chars...",
    "computed_hash": "64-hex-chars...",
    "verified_at": "2026-01-01T00:00:00Z"
  }
}
```

`status` is one of `VERIFIED` (the hashes match) or `INTEGRITY_FAILURE`
(they don't) — **both are a `200`**, not an error: verification
*answering the question correctly* is success, regardless of which answer
it finds. `stored_hash`/`computed_hash` are always both present, hex
lowercase, so a caller can see exactly what diverged; no other storage
detail (bucket, object key) is ever exposed. The canonical
`documents.sha256_hash` column is **never** rewritten by this endpoint,
in either outcome — a discovered mismatch is reported, never "repaired".
The only column verification may update is `documents.status` (moving to
`TAMPERED` on a mismatch, or back to `ACTIVE` if a previously-tampered
document now verifies again — always reflecting the *current* truth, and
only written when it actually needs to change).

- `403` — no relationship to the document's case, OR the document doesn't
  exist, OR the `:id` isn't a valid UUID — identical response in all
  three cases, same anti-enumeration posture as download.
- `503` — the object could not be retrieved/hashed at all (storage
  outage, object missing) — a **storage error**, categorically different
  from `INTEGRITY_FAILURE` above and never conflated with it (see
  `docs/SECURITY.md`'s "Tamper detection").

Every completed verification records an audit event: `DOCUMENT_VERIFIED`
on a match, `DOCUMENT_INTEGRITY_FAILURE` on a mismatch (with both hashes
in the event metadata, never raw file bytes) — see "Audit" in
`docs/SECURITY.md`.

### `GET /api/v1/documents/:id/certificate` (implemented — System 7)

Requires `Authorization: Bearer <access_token>` plus `certificate:read`
(RBAC) and `CanAccessDocument` (ABAC). Per the seed data, JUDGE and ADMIN
hold `certificate:read`; POLICE/FORENSICS/LAWYER do not.

This single endpoint both retrieves an existing certificate and, for a
caller who *also* holds `certificate:create` for this document (ADMIN
only, per the seed data — ADMIN is the only role holding both), generates
one on demand if none exists yet. There is deliberately no separate
`POST` create route.

```json
{
  "success": true,
  "data": {
    "id": "...",
    "document_id": "...",
    "document_hash": "64-hex-chars...",
    "certificate_version": "1.0",
    "signature_algorithm": "ECDSA-P256-SHA256",
    "signature": "hex-encoded ASN.1 DER...",
    "issuer": "Evidentia",
    "generated_by": "...",
    "generated_at": "2026-01-01T00:00:00Z"
  }
}
```

`document_hash` is the *exact* hash the document had at generation time —
the certificate's cryptographic binding, per `docs/SECURITY.md`'s
"Compliance certificates". The signing **private** key is never reachable
through this or any other response.

Three outcomes, controlled entirely server-side by the caller's own
permissions (never leaked to the client as a distinguishable response):

1. An existing certificate bound to the document's *current* canonical
   hash is returned (`200`).
2. None exists, but the caller also holds `certificate:create`: one is
   generated — the document's hash is recomputed from the stored object
   and compared against the canonical hash again (never trusting an
   earlier check), and only on a match is a certificate created, signed
   over a canonical (fixed-field-order, never arbitrary JSON) payload,
   and persisted (`200`).
3. None exists and the caller lacks `certificate:create`: `404` — 
   indistinguishable from "not generated yet", so a `certificate:read`-only
   caller never learns whether they'd be allowed to generate one.

- `403` — no relationship to the document's case, OR the document doesn't
  exist, OR the `:id` isn't a valid UUID, OR the caller holds neither
  `certificate:read` nor `certificate:create` — identical response in
  every case.
- `404` — outcome 3 above.
- `409` — the document failed integrity verification (recomputed hash no
  longer matches the canonical hash): **a tampered document can never
  receive a valid certificate**. The canonical hash is not rewritten; the
  document's `status` may move to `TAMPERED` (same reconciliation
  `POST /documents/:id/verify` performs).
- `503` — the object could not be retrieved/hashed at all (storage error,
  not an integrity finding).

Concurrent "generate a certificate for this document" requests are safe:
a database-level uniqueness constraint on
`(document_id, document_hash)` (`compliance_certificates_document_hash_unique`,
`db/migrations/000003_certificate_integrity.up.sql`) means only one
`INSERT` can ever win; a losing concurrent request transparently fetches
and returns the winning row rather than erroring or duplicating.

Every certificate generation records a `CERTIFICATE_CREATED` audit event;
a discovered mismatch during generation records `DOCUMENT_INTEGRITY_FAILURE`
(same as verification) instead — see "Audit" in `docs/SECURITY.md`.

### `POST /api/v1/documents/:id/redact` (implemented)

Requires `Authorization: Bearer <access_token>` plus `document:redact`
(RBAC) and `CanAccessDocument` (ABAC) — the same document-scoped
authorization pattern as verify/download, re-checked independently at the
service layer (`DocumentService.RedactDocument`). Per the seed data,
**only ADMIN** holds `document:redact` today — a deliberate, existing
System 4 policy decision (see `backend/tests/rbac_test.go`'s
`TestRBAC_PolicePermissions`), not something this endpoint expands.

```json
{
  "reason": "Protect witness identity",
  "regions": [
    { "page": 1, "x": 100, "y": 200, "width": 150, "height": 40 }
  ]
}
```

`:id` is the **source** document. `reason` is required (3-2000 UTF-8
characters). `regions` is required (1-50 entries); each region's
coordinates are in the source image's own pixel space and are validated
both structurally (non-negative, finite, positive area, `page == 1`) and,
once the source is decoded, against its **actual** pixel dimensions.

Produces a brand-new, independent `documents` row (`parent_document_id`
pointing at the source) plus a linked `redactions` row — **never**
modifies the source document's row, object, `sha256_hash`, or any existing
certificate. Supported formats: `image/png` and `image/jpeg` only — every
region is destructively overwritten (opaque black, a straight pixel
replace, never an alpha-blended overlay) in the derivative's re-encoded
bytes before it is hashed and stored, so the "redacted" content is not
recoverable through normal viewing/extraction of the derivative. Any other
stored `mime_type` (including `application/pdf`) is refused with `422` —
this project has no verified, safe way to strip underlying content from
those formats yet, and a fake redaction (a box drawn merely on top of
unmodified content) would be strictly worse than refusing outright.

Before processing, the source document's *current* bytes are recomputed
and compared against its canonical hash — the same anti-tamper check
`GET /documents/:id/certificate`'s generation path performs — refusing
with `409` on a mismatch rather than deriving a "redacted" copy from
unknown/corrupted bytes.

```json
{
  "success": true,
  "data": {
    "redaction_id": "...",
    "source_document_id": "...",
    "reason": "Protect witness identity",
    "created_at": "2026-01-01T00:00:00Z",
    "document": {
      "id": "...",
      "case_id": "...",
      "document_type": "WITNESS_STATEMENT",
      "filename": "redacted_witness.png",
      "mime_type": "image/png",
      "file_size": 4096,
      "sha256_hash": "64-hex-chars... (H2 — always different from the source's H1)",
      "status": "ACTIVE",
      "parent_document_id": "...(the source document's ID)",
      "uploaded_by": "...",
      "uploaded_at": "2026-01-01T00:00:00Z"
    }
  }
}
```

The derivative is a completely ordinary document from every other route's
perspective: `POST /documents/{derivative_id}/verify` and
`GET /documents/{derivative_id}/certificate` work unmodified, bound to the
derivative's own hash, independent of the source's. Access to the
derivative is controlled by the **same** case-relationship rule as any
document — it never becomes more broadly accessible merely because it was
produced by a redaction.

- `400` — missing/too-short/too-long `reason`, no regions, too many
  regions, or a region with negative/non-finite/non-positive/
  out-of-bounds coordinates.
- `403` — no relationship to the document's case, OR the document doesn't
  exist, OR the `:id` isn't a valid UUID, OR the caller lacks
  `document:redact` — identical response in every case.
- `409` — the source document failed integrity verification.
- `422` — the source document's `mime_type` has no supported redaction
  implementation.
- `503` — the source object could not be retrieved from storage.

Every successful redaction records a `DOCUMENT_REDACTED` audit event
(source/result document IDs, reason, region count, both hashes — never
raw file bytes); a discovered mismatch during the pre-processing integrity
check records `DOCUMENT_INTEGRITY_FAILURE` instead — see "Audit" in
`docs/SECURITY.md`.

### `POST /api/v1/documents/:id/share` (implemented)

Requires `Authorization: Bearer <access_token>` plus `document:share`
(RBAC) and `CanAccessDocument` (ABAC) — the same document-scoped
authorization pattern as redact, re-checked independently at the service
layer (`ShareService.CreateShare`). Grants `user_id` a specific, revocable,
optionally time-bounded permission on **this exact document** — never
ownership, never resharing (a share's own recipient can never call this
route successfully using their delegated access, regardless of
permission tier — see below).

```json
{
  "user_id": "recipient-user-id",
  "permission": "VIEW",
  "expires_at": "2026-12-31T23:59:59Z",
  "reason": "Disclosure to defence counsel"
}
```

`permission` is `VIEW` (grants `document:read` + `document:download` +
`certificate:read` — this application has no distinct metadata-only view
separate from download) or `VERIFY` (VIEW's grants plus
`document:verify`) — never `EDIT`/`DELETE`/`REDACT`/`RESHARE`. `expires_at`
is optional (omitted/`null` = non-expiring); if present it must be in the
future. `reason` is optional. The recipient must be a real, currently
**active** Evidentia user (found via `GET /users/search` below) and
cannot be the caller themself.

```json
{
  "success": true,
  "data": {
    "share_id": "...",
    "document_id": "...",
    "recipient_user_id": "...",
    "created_by_user_id": "...",
    "permission": "VIEW",
    "status": "ACTIVE",
    "effective_status": "ACTIVE",
    "expires_at": "2026-12-31T23:59:59Z",
    "created_at": "2026-01-01T00:00:00Z"
  }
}
```

- `400` — invalid `permission`/`expires_at` (in the past)/recipient
  (nonexistent, inactive, or the caller themself).
- `403` — no relationship to the document's case, OR the document
  doesn't exist, OR the caller lacks `document:share` — identical
  response in every case (this is also what a share's own recipient gets
  if they try to reshare: `document:share` is never satisfiable through
  delegated access, at any permission tier).
- `409` — an ACTIVE share already exists for this exact
  (document, recipient) pair — enforced by a database-level partial
  unique index, not just an application check.

Every successful share records a `DOCUMENT_SHARED` audit event (document
ID, recipient ID, permission — never raw file content); see "Audit" in
`docs/SECURITY.md`.

### `GET /api/v1/documents/:id/shares` (implemented)

Requires `document:share` + `CanAccessDocument` — the same authority
needed to create a share. Returns every share ever created for the
document, newest first, **including revoked/expired ones** (historical
delegation records are never hidden — only `effective_status` changes to
reflect the current truth: `ACTIVE`, `EXPIRED`, or `REVOKED`).

```json
{ "success": true, "data": { "shares": [ { "share_id": "...", "...": "..." } ] } }
```

A mere recipient (not a case member, not the creator, not ADMIN) gets
`403` here even for a document shared with them — seeing who ELSE a
document is shared with is a materially different, more sensitive
capability than merely using one's own delegated access.

### `POST /api/v1/documents/:id/shares/:shareId/revoke` (implemented)

Requires `document:share` + `CanAccessDocument` (the same authority as
creating a share — not narrowed to only the share's original creator).
`shareId` is scoped to `:id` as well as its own ID: a real share ID
belonging to a **different** document is denied identically to a
nonexistent one. Transitions the share `ACTIVE -> REVOKED` — immediately
and permanently; there is no un-revoke. Access via that share is denied
server-side starting with the very next request the recipient makes
(never a frontend-only removal).

- `403` — no relationship to the document's case, the document doesn't
  exist, or the caller lacks `document:share`.
- `404` — the share doesn't exist, doesn't belong to this document, or
  was already revoked — one generic response for all three, never
  distinguishing which.

Every successful revocation records a `DOCUMENT_SHARE_REVOKED` audit
event.

### `GET /api/v1/shared/documents` (implemented — "Shared With Me")

Requires authentication only. Lists every document for which the caller
currently holds an **active, unexpired** share — never a document reached
through case membership alone. This is the recipient's discovery
mechanism: there is no standalone `GET /documents/:id`, so a recipient
with no relationship to the document's case has no other way to learn
its filename/type/hash.

```json
{
  "success": true,
  "data": {
    "documents": [
      {
        "share_id": "...",
        "permission": "VIEW",
        "expires_at": null,
        "shared_at": "2026-01-01T00:00:00Z",
        "shared_by_user_id": "...",
        "document": { "id": "...", "filename": "...", "...": "..." }
      }
    ],
    "meta": { "page": 1, "page_size": 20, "total": 1, "total_pages": 1 }
  }
}
```

### `GET /api/v1/users/search` (implemented — recipient picker)

Requires authentication only — deliberately **not** the admin-only
`user:read` permission (`GET /admin/users`): this is a narrower, safer
capability any authenticated user needs to find a share recipient.
`q` (required, minimum 2 characters) matches against email/name
(case-insensitive substring); results are capped at 10, exclude inactive
users, and exclude the caller themself. Returns only `id`, `first_name`,
`last_name`, `display_name`, `email`, `roles` — no phone, status, or
timestamps.

- `400` — `q` missing, too short, or too long.

### Redaction & sharing lineage

A redacted derivative (System 8) is a completely ordinary document from
sharing's perspective: it can be shared independently of its source, and
sharing it **never** grants access to the source, or vice versa — see
`docs/SECURITY.md`'s "Document Sharing" for why (each document's access is
resolved independently; there is no "inherit the parent's shares" rule
anywhere in this system).

### Not yet implemented

`GET /documents/:id` (standalone metadata — today's only exposure of
document metadata for a case-member is via `GET /cases/:id`'s
`documents` array, per master prompt §27's fallback: "otherwise expose
document metadata through the existing case detail flow"; a share
recipient instead uses `GET /shared/documents` above). Required
authorization, once implemented:

| Route | Permission (RBAC) | Resource check (ABAC) |
|---|---|---|
| `GET /documents/:id` | `document:read` | `CanAccessDocument` |

`CanAccessDocument` resolves the document's case and applies the same
case-relationship check as `CanAccessCase` — see `docs/SECURITY.md`'s
"Document-based ABAC".

## Audit (implemented — Systems 8 & 11)

```text
GET  /audit
POST /audit/verify-chain
GET  /audit/verify-chain/:verificationId
GET  /audit/verify-chain/:verificationId/events   (SSE)
GET  /audit/verifications
GET  /audit/integrity
```

See [AUDIT_CHAIN.md](./AUDIT_CHAIN.md) for the full hash-chain and
asynchronous-verification architecture; this section covers the HTTP
contract.

`GET /audit` needs `audit:read`. Every other route below needs
`audit:verify`. Per the seed data, ADMIN, POLICE, LAWYER, and JUDGE hold
`audit:read` — but row-level visibility beyond that permission check is
entirely PostgreSQL RLS's job (`audit_log_select`, see AUDIT_CHAIN.md's
"Row-Level Security"), not a route-level ABAC middleware: this route has
no single case/document ID in its URL to check against the way
`RequireCaseAccess`/`RequireDocumentAccess` do for other resources, since
it's a filtered LISTING (like `GET /cases`). ADMIN sees every entry;
every other role sees only its own actions plus entries tied to a case it
is an active member of — a query filter can only narrow this further,
never widen it. Only ADMIN holds `audit:verify` — verifying/inspecting
the chain only makes sense against its complete, unfiltered state.

`GET /audit` query parameters (all optional; a filter can only narrow
what RLS already permits): `user_id`, `role`, `action`, `resource_type`,
`resource_id`, `case_id` (UUIDs/exact strings), `from`/`to` (RFC3339
timestamps, half-open range), `page`/`page_size` (pagination, required —
this endpoint never returns the whole table). Response: the standard
envelope wrapping `{entries: [...], meta: {...}}`, where each entry's
`hash`/`prev_hash` are lowercase-hex-encoded (`prev_hash` empty only for
the genesis entry).

### `POST /audit/verify-chain` (System 11 — asynchronous; System 12 — background-job infrastructure)

No request body. Dispatches a background job rather than verifying
synchronously — see AUDIT_CHAIN.md's "Asynchronous Verification" for why,
and BACKGROUND_JOBS.md for the job infrastructure itself. Always `202
Accepted`:

```json
{ "verification_id": "...", "job_id": "audit:verify_chain:...", "status": "QUEUED", "created_at": "..." }
```

`job_id` is the underlying Asynq task's deterministic, traceable ID
(`jobs.AuditVerifyChainJobID(verification_id)` — always derivable from
`verification_id` alone, never a separately-stored value). If a
verification is already `QUEUED`/`RUNNING`, this returns **that same
run's** id/job_id/status instead of starting a duplicate scan — two
concurrent callers are never given two independent verification ids.

### `GET /audit/verify-chain/:verificationId`

Current status/progress/result of one run:

```json
{
  "verification_id": "...", "job_id": "audit:verify_chain:...", "status": "RUNNING",
  "entries_checked": 42381, "total_entries": 100000, "progress_percent": 42.4,
  "last_seq_checked": 42381,
  "requested_by_user_id": "...", "requested_by_role": "ADMIN",
  "started_at": "...", "completed_at": null,
  "created_at": "...", "updated_at": "..."
}
```

On `INTEGRITY_FAILURE`: additionally carries `failed_entry_id`,
`failed_seq`, `failure_type` (e.g. `ENTRY_HASH_MISMATCH`), and a safe
`failure_reason` string — never metadata content, SQL text, or a
filesystem path. On `FAILED`: `failure_type` is an operational category
(`DATABASE_ERROR`, `TIMEOUT`, `STALE_TIMEOUT`) — never confused with an
integrity finding. `404` for an unknown/inaccessible id (RLS makes "exists
but not yours" and "doesn't exist" indistinguishable for a non-ADMIN
caller, though in practice only ADMIN can ever create one).

### `GET /audit/verify-chain/:verificationId/events` (SSE — System 13 infrastructure)

`text/event-stream` of `AUDIT_VERIFICATION_STARTED` /
`AUDIT_VERIFICATION_PROGRESS` / `AUDIT_VERIFICATION_COMPLETED` /
`AUDIT_INTEGRITY_FAILURE` / `AUDIT_VERIFICATION_FAILED` events, each
following System 13's one event envelope (`event_id`, `event_type`,
`event_version`, `timestamp`, `resource_type`, `resource_id`, `data`  —
see `docs/REALTIME_EVENTS.md`). `data` carries a safe, progress-focused
subset of the plain status endpoint's own fields — `verification_id`,
`status`, `entries_checked`, `total_entries`, `progress_percent`,
`failed_entry_id`, `failure_type`, `failure_reason` — using the exact
same field names (never renamed/reshaped) so client code can read either
shape without a translation layer. Authenticated exactly like
every other route (a
normal `Authorization: Bearer` header — no token in the URL); sends the
current state immediately on connect, then relays further events until a
terminal one is sent or the client disconnects. See AUDIT_CHAIN.md's "SSE
architecture" for the full connection-management/reconnection contract.

### `GET /audit/verifications`

Paginated history. Query parameters: `status`, `requested_by` (UUID),
`from`/`to` (RFC3339), `page`/`page_size`. Response: `{verifications: [...
same shape as GET /verify-chain/:id ...], meta: {...}}`.

### `GET /audit/integrity`

Dashboard summary — cheap aggregate data, never a fresh scan:

```json
{
  "total_entries": 100234,
  "chain_head_seq": 100234, "chain_head_hash": "…64 hex chars…",
  "last_verification": { "verification_id": "...", "status": "VERIFIED", ... }
}
```

## Admin (implemented)

All under `/api/v1/admin`, plus `/api/v1/users/me`. See
`internal/service.UserService` and `internal/handlers/user`.

```text
POST /api/v1/admin/users
GET  /api/v1/admin/users
GET  /api/v1/admin/users/:id
PUT  /api/v1/admin/users/:id
PUT  /api/v1/admin/users/:id/role
PUT  /api/v1/admin/users/:id/status
PUT  /api/v1/admin/users/:id/password
GET  /api/v1/admin/roles
GET  /api/v1/users/me
```

Required authorization: `POST /admin/users`/`GET /admin/users`/`GET
/admin/users/:id` need `user:create`/`user:read`/`user:read` respectively;
`PUT /admin/users/:id` needs `user:update`; `PUT /admin/users/:id/role`
needs `internal/authz.Service.CanModifyUserRole` (RBAC `user:role` PLUS an
explicit block on an actor modifying their own role — see
`docs/SECURITY.md`'s "Privilege escalation / admin boundaries"); `PUT
/admin/users/:id/status` needs `user:deactivate` PLUS the same kind of
block on an actor changing their own status; `PUT
/admin/users/:id/password` reuses `user:update` (no separate password
permission); `GET /admin/roles` and `GET /users/me` need no special
permission beyond authentication — the roles route lists the fixed,
non-sensitive role catalog, and every user may view their own profile
regardless of role. Per the seed data, only ADMIN holds `user:create`/
`user:read`/`user:update`/`user:deactivate`/`user:role` today.

`PUT /admin/users/:id/status` revokes every one of the target user's
refresh sessions when the new status isn't `active`; `PUT
/admin/users/:id/password` always revokes them. Neither route nor any
other in this section ever returns a password or password hash.

The one account provisioned outside this flow is the initial bootstrap
admin (`internal/bootstrap.EnsureBootstrapAdmin`, run once at server
startup) — see the root `.env.example`'s `EVIDENTIA_BOOTSTRAP_ADMIN_*`
variables and that function's doc comment.

## Health and Readiness (implemented)

These two endpoints are infrastructure probes, not domain API — they return
a flat JSON shape rather than the standard envelope below, and sit outside
`/api/v1` (see `internal/handlers/health`).

```http
GET /health
```

```json
{"status": "ok", "service": "evidentia-backend", "version": "dev"}
```

Always cheap; never touches PostgreSQL, Redis, or MinIO.

```http
GET /ready
```

```json
{"status": "ready", "dependencies": {"postgres": "ok", "redis": "ok", "minio": "ok"}}
```

Returns `200` when every dependency is reachable, `503` otherwise (with the
failing dependency marked `"error"` — never a raw driver error or
connection string).

## Response Envelope

Every other endpoint uses the standard envelope — see
[../backend/pkg/response/response.go](../backend/pkg/response/response.go):

```json
{"success": true, "data": {}}
{"success": false, "error": {"code": "NOT_FOUND", "message": "...", "request_id": "..."}}
```

Unmatched routes and disallowed methods already return this envelope today
(`NOT_FOUND` / `404`, `METHOD_NOT_ALLOWED` / `405`), implemented in
`internal/httpserver/router.go`.

## Authentication & Authorization Requirements

`internal/middleware.Auth` validates a request's JWT and re-resolves the
caller's current account status/roles from the database (never trusting
the JWT's role claim alone — see SECURITY.md) — this establishes
*identity*, not *authorization*.

System 4 (`internal/authz`) provides the RBAC (`middleware.RequirePermission`)
and ABAC (`middleware.RequireCaseAccess`/`RequireDocumentAccess`) checks
layered on top of it, and the per-route requirements are documented inline
above (Cases/Case Documents/Documents/Audit/Admin). Cases, Case Documents
(upload/download/redact/share), document verify/certificate, Admin (user
management), and Audit routes are all live and wired with exactly that
middleware (see `internal/httpserver/router.go`). Today's non-health
routes are `/api/v1/auth/{login,refresh,logout}` (no RBAC/ABAC — see
"Authentication" above), `/api/v1/cases`/`/api/v1/cases/:id`,
`/api/v1/cases/:id/documents`, `/api/v1/documents/:id/download`,
`/api/v1/documents/:id/verify`, `/api/v1/documents/:id/certificate`,
`/api/v1/documents/:id/redact`, `/api/v1/documents/:id/share`,
`/api/v1/documents/:id/shares*`, `/api/v1/shared/documents`,
`/api/v1/audit`, `/api/v1/audit/verify-chain`, `/api/v1/admin/users*`,
`/api/v1/admin/roles`, and `/api/v1/users/me`.
Full authorization design: `docs/SECURITY.md`'s Authorization section.

`401` vs `403`: a request with no/invalid/expired authentication is
always `401 UNAUTHORIZED`; an authenticated request denied by RBAC or ABAC
is `403 FORBIDDEN` with the generic message `"You do not have permission
to perform this action"` — never a message naming the specific
permission, case, or document relationship that failed.
