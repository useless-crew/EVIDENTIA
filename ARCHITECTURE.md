# Evidentia — High-Level Architecture

> This document describes the **intended** architecture. Systems 1-7 —
> Foundation & Infrastructure, Database & Data Layer, Authentication &
> Session Security, Authorization (RBAC + ABAC + RLS integration), Case
> Management, Document Management & Evidence Ingestion, and Evidence
> Verification/Tamper Detection/Compliance Certificates — are implemented
> (see below); everything under Request Flow past them is still a design
> reference. Note that System 4 implements the *authorization
> infrastructure* only — the audit/admin HTTP routes it will eventually
> guard are a later system's scope and are not registered yet (see the
> System 4 section below).

## System 1 — Foundation & Infrastructure (Implemented)

```text
cmd/server/main.go
    |
    v
internal/app.New(ctx)
    |
    +--> internal/config.Load()     — typed, validated env config
    +--> internal/logger.New()      — structured slog logger
    +--> internal/database.New()    — pgx pool + Ping
    +--> internal/cache.New()       — go-redis client + Ping
    +--> internal/storage.NewMinIO()— MinIO client + bucket ensure
    |
    v
*app.App  (DI container: Config, Logger, DB, Cache, Storage)
    |
    v
internal/httpserver.NewRouter(app)
    |
    +--> middleware.Recovery       — panic -> safe JSON, logs stack server-side
    +--> middleware.RequestID      — validates/generates X-Request-ID
    +--> middleware.RequestLogger  — structured per-request log line
    +--> middleware.CORS           — configured origins/methods/headers
    +--> middleware.BodyLimit      — caps request body size
    |
    +--> GET /health   (handlers/health.Liveness)
    +--> GET /ready    (handlers/health.Readiness — pings DB/Cache/Storage)
    +--> NoRoute/NoMethod -> standard error envelope
```

`app.App` depends on `DBConn`/`CacheConn` interfaces (declared in
`internal/app`), not the concrete `*database.Database`/`*cache.Cache`
types — this is what lets tests substitute fakes for Postgres/Redis/MinIO
without Docker (see `internal/httpserver/router_test.go`).

Shutdown, triggered by SIGINT/SIGTERM: stop accepting new connections
(`http.Server.Shutdown`, bounded by `SERVER_SHUTDOWN_TIMEOUT`) → close
Redis → close the PostgreSQL pool → exit (`cmd/server/main.go`).

## System 2 — Database & Data Layer (Implemented)

```text
backend/db/migrations/000001_init_schema.{up,down}.sql
    |
    v
12 tables, RLS policies, current_app_user_id()/current_app_role(),
the evidentia_app runtime role (least-privilege, NOSUPERUSER, NOBYPASSRLS)
    |
    v
backend/db/queries/*.sql  --sqlc generate-->  backend/db/generated/
    |
    v
internal/models   (type aliases + controlled-vocabulary constants)
    |
    v
internal/repository
    |
    +--> UserRepo, CaseRepo, DocumentRepo, AuditRepo, CertificateRepo
    |      (thin wrappers over *generated.Queries)
    |
    +--> WithTx(ctx, pool, AppIdentity{UserID, Role}, fn)
           |
           v
         BEGIN -> set_config('app.user_id', ..., true)
                  set_config('app.role', ..., true)
               -> fn(ctx, q)
               -> COMMIT / ROLLBACK
```

`cmd/migrate` (a separate binary from `cmd/server`) applies migrations
using `DATABASE_MIGRATOR_USER`/`PASSWORD` — a privileged, schema-owning
role distinct from `evidentia_app`, which is all `cmd/server` ever
connects as. See [docs/DATABASE_SCHEMA.md](./docs/DATABASE_SCHEMA.md) for
the full schema, RLS design, and privilege model — including the audit-log
immutability guarantee verified in
`backend/tests/db_audit_privileges_test.go`.

`internal/database.Database` (System 1) still owns the connection pool
itself; System 2 adds the transaction/RLS-identity layer on top of it.
System 3's `AuthService` is the first code to actually query `users` and
(via its own migration) `auth_sessions` through this layer — see below.

## System 3 — Authentication & Session Security (Implemented)

```text
POST /api/v1/auth/{login,refresh,logout}
    |
    v
internal/handlers/auth   — bind/validate request, shape response
    |
    v
internal/service.AuthService
    |
    +--> internal/auth
    |      HashPassword/VerifyPassword (bcrypt)
    |      JWTManager.CreateAccessToken/Validate (HS256, golang-jwt/v5)
    |      GenerateRefreshToken/HashRefreshToken (crypto/rand + SHA-256)
    |      AuthenticatedUser + SetAuthenticatedUser/CurrentUser (gin context)
    |
    +--> internal/repository (System 2's WithTx + UserRepo/AuthSessionRepo)
    |      users, auth_sessions — via db/migrations/000002_auth_sessions
    |
    +--> internal/audit.Recorder (SlogRecorder — logs, does not persist
           to audit_log; System 8 provides the durable implementation)

internal/middleware.Auth (guards POST /auth/logout; later systems' routes
will use it too)
    |
    +--> JWTManager.Validate     — signature/algorithm/issuer/audience/exp
    +--> AuthService.ResolveIdentity — fresh status+role lookup, never
           trusting the JWT's role claim
    +--> auth.SetAuthenticatedUser  — attaches AuthenticatedUser to the
           gin.Context for downstream handlers (and, later, System 4)
```

`app.App` gained `JWTManager` and `AuthService` fields (constructed in
`app.New`, alongside the System 1 infrastructure clients) — `NewRouter`
wires them into the `/api/v1/auth` group and into `middleware.Auth`.
`middleware.Auth` depends on an `IdentityResolver` *interface* (satisfied
structurally by `*service.AuthService`), not the concrete service type —
the same testability pattern System 1 used for `app.DBConn`/`CacheConn`,
letting the middleware be unit-tested with a fake and no database. Full
design/security rationale: [docs/SECURITY.md](./docs/SECURITY.md).

## System 4 — Authorization: RBAC + ABAC + PostgreSQL RLS Integration (Implemented)

```text
internal/authz.Service
    |
    +--> HasPermission(ctx, user, action)          — RBAC
    |      loads user.Roles' permissions from roles/permissions/
    |      role_permissions (System 2), unioned across every role
    |
    +--> CanAccessCase(ctx, user, caseID, action)   — ABAC
    +--> CanAccessDocument(ctx, user, docID, action)— ABAC
    |      HasPermission first (cheap, no resource lookup), then loads the
    |      resource under the CALLER'S OWN transaction-local RLS identity
    |      (repository.WithTx) and independently re-derives owner/member
    |      status from the returned rows
    |
    +--> CanModifyUserRole(ctx, actor, targetUserID) — privilege-escalation guard
    +--> CanViewProtectedPartyDetails / SanitizeInvolvedParty — witness-identity policy
    |
    +--> internal/audit.Recorder — AUTHZ_DENIED events on every denial

internal/middleware.RequirePermission(authorizer, action)
    — RBAC gate: 401 (no authenticated user) / 403 (denied) / 500 (authorizer
      error) / next handler

internal/middleware.RequireCaseAccess / RequireDocumentAccess(authorizer, action, param)
    — ABAC gate: parses the path param as a UUID (malformed -> 403, same
      as a real denial), calls the matching Can* method, same status
      mapping as above
```

`app.App` gained an `AuthzService *authz.Service` field (constructed in
`app.New`, sharing the same `*pgxpool.Pool` and `audit.Recorder` as
`AuthService`). `internal/authz` depends only on `internal/auth`
(`AuthenticatedUser`) and `internal/repository` (`WithTx`) — no import
cycle, and no new external dependency (go.mod unchanged).

**What's genuinely new here** vs. what System 2 already built: System 2
already implemented full PostgreSQL RLS (`current_app_user_id()`/
`current_app_role()`, policies on every case/document-adjacent table) and
the transaction-local identity plumbing (`repository.WithTx`). System 4
adds the *application-layer* RBAC/ABAC decision engine that composes with
that RLS as defense-in-depth (neither layer trusts the other blindly —
see `docs/SECURITY.md`'s "PostgreSQL RLS integration"), the authorization
middleware, the witness-identity policy, and the privilege-escalation
guard for role modification.

**What's deliberately not here yet**: `internal/handlers/{case,document,
audit,user}` and `internal/service/{case,document,audit,user}_service.go`
remain TODO stubs (later systems' business logic), so no case/document/
audit/admin route exists in `internal/httpserver/router.go` for this
middleware to guard yet. `router.go` carries a comment showing the
intended wiring for whichever system adds those routes. Full design:
[docs/SECURITY.md](./docs/SECURITY.md); full test coverage (RBAC matrix,
ABAC case/document relationships, IDOR, privilege escalation, header/role
spoofing): `backend/internal/authz/*_test.go`,
`backend/internal/middleware/{rbac,abac}_middleware_test.go`,
`backend/tests/{rbac,abac}_test.go`.

## System 5 — Case Management & Case Lifecycle (Implemented)

```text
internal/handlers/case (package cases)
    |
    +--> Create/List/Get/Update — parse/validate request, read the
    |      already-authenticated user, delegate to CaseService, shape the
    |      response. No SQL, transaction, or audit write here.
    v
internal/service.CaseService
    |
    +--> independently re-checks authz.Service.HasPermission/CanAccessCase
    |      (service-layer authorization — see docs/SECURITY.md; not just
    |      trusting that HTTP middleware already ran)
    +--> validates input (case_number/title/description length, status
    |      enum, JSONB metadata shape/size — internal/utils.ValidateJSONMetadata)
    +--> enforces its OWN documented status-transition model
    |      (caseStatusTransitions) — System 2's schema only constrains the
    |      value set, not a transition graph
    +--> internal/repository.CaseRepo (ListFiltered/CountFiltered — new
    |      sqlc queries; Create/GetByID/Update/AddMember — existing)
    |      via repository.WithTx (transaction-local RLS identity)
    +--> internal/audit.Recorder — CASE_CREATED / CASE_UPDATED /
           CASE_STATUS_CHANGED events (never a false "success" event for a
           failed/rolled-back mutation)
```

`app.App` gained a `CaseService *service.CaseService` field. Routes
registered in `internal/httpserver/router.go` under `/api/v1/cases`:

```text
POST   /api/v1/cases      Auth + RequirePermission(case:create)
GET    /api/v1/cases      Auth + RequirePermission(case:read)
GET    /api/v1/cases/:id  Auth + RequireCaseAccess(case:read, "id")
PUT    /api/v1/cases/:id  Auth + RequireCaseAccess(case:update, "id")
```

— exactly the wiring System 4's `router.go` comment already sketched.

**Role-scoped listing** (`GET /cases`) is enforced entirely by PostgreSQL
RLS (System 2's `cases_select` policy), not by Go-side filtering: a
POLICE/LAWYER/FORENSICS/JUDGE caller's query only ever returns cases they
created or hold an active `case_members` row for; ADMIN sees all. The two
new sqlc queries (`ListCasesFiltered`/`CountCasesFiltered`,
`db/queries/cases.sql`) add optional status/case_number/title/created_by/
created_from/created_to filtering and pagination LIMIT/OFFSET entirely in
SQL, on top of whatever RLS already narrowed the result set to — never
"select everything, filter in Go". No docket table exists for JUDGE's
"authorized scope" — the safest supported interpretation (the same
`case_members` mechanism every other non-role uses) is implemented, with
finer-grained docket enforcement explicitly deferred (see
`docs/SECURITY.md`'s "Case-based ABAC").

**Case detail** (`GET /cases/:id`) assembles metadata, status,
witness-identity-sanitized involved parties (`authz.SanitizeInvolvedParty`
— reused as-is from System 4, now finally wired into a live handler),
document references (metadata only — never bytes, never MinIO), and a
chronological timeline synthesized from already-loaded case/document/
involved-party timestamps. It deliberately does NOT read `audit_log`: no
system populates that table yet (`audit.SlogRecorder` still writes only to
the operational log — System 8 owns the durable, hash-chained writer), so
building a timeline from it would either be empty or require this system
to invent chain-writing logic explicitly out of scope.

**IDOR posture** matches System 4's existing middleware exactly:
`CaseService.GetCase`/`UpdateCase` return the identical `403 FORBIDDEN`
generic-message error for a case that doesn't exist and a case the caller
has no relationship to (verified by `TestCaseFlow_EndToEnd`'s guessed-UUID
and malformed-UUID assertions).

**What's genuinely new here** vs. System 4: the first live case/document
handler package, the first `CaseService`, two new sqlc queries, no new
migration (System 2's `cases`/`case_members`/`case_involved_parties`/
`documents` schema and indexes were already sufficient), and no change to
any RLS policy or `evidentia_app` grant.

**What's deliberately not here**: document upload/download/verify/redact/
share, audit-chain computation/verification, and admin user management
remain later systems' scope — `internal/handlers/{document,audit,user}`
and their services remain TODO stubs. Full design:
[docs/SECURITY.md](./docs/SECURITY.md); tests:
`backend/internal/service/case_service_integration_test.go`,
`backend/internal/httpserver/case_flow_integration_test.go`,
`backend/tests/case_rls_test.go`.

## System 6 — Document Management & Evidence Ingestion (Implemented)

```text
internal/handlers/document.Upload (multipart, streaming)
    |
    +--> http.Request.MultipartReader — true stream, never
    |      ParseMultipartForm/FormFile's buffer-to-memory-or-tempfile
    v
internal/service.DocumentService.UploadDocument
    |
    +--> authz.Service.CanAccessCase(ctx, user, caseID, ActionDocumentUpload)
    |      — RBAC (document:upload) + case ABAC in ONE existing call,
    |        no new authorization code
    +--> validate document_type/description; sanitize filename
    +--> generate document UUID; build object key
    |      cases/{case_id}/documents/{document_id}/original
    +--> stream file: io.TeeReader -> pkg/hash.New() + Storage.Put
    |      (limitedReader aborts past DocumentsConfig.MaxUploadSize)
    +--> repository.WithTx: CreateDocument (id, hash, bucket, key, ...)
    |      on failure: best-effort Storage.Delete (orphan cleanup),
    |      logged operationally if that also fails
    +--> audit.Recorder — DOCUMENT_UPLOADED

internal/handlers/document.Download (streaming response)
    |
    v
internal/service.DocumentService.DownloadDocument
    |
    +--> authz.Service.CanAccessDocument(ctx, user, docID, ActionDocumentDownload)
    +--> repository.WithTx: GetDocumentByID (under RLS)
    +--> Storage.Get  ← only after the above two succeed, never before
    +--> audit.Recorder — DOCUMENT_DOWNLOADED
    +--> gin DataFromReader — streams to HTTP response
```

`app.App` gained a `DocumentService *service.DocumentService` field
(constructed in `app.New`, sharing `AuthzService`/`audit.Recorder` with
`CaseService`, plus the existing `Storage` and a new
`config.DocumentsConfig.MaxUploadSize` — `MAX_UPLOAD_SIZE`, default 50
MiB, deliberately independent of `ServerConfig.MaxBodyBytes` since two
`http.MaxBytesReader` wrappings on one request compose by taking the
smaller limit). Routes registered in `internal/httpserver/router.go`:

```text
POST /api/v1/cases/:id/documents      Auth + RequireCaseAccess(document:upload, "id") + its own BodyLimit
GET  /api/v1/documents/:id/download   Auth + RequireDocumentAccess(document:download, "id")
```

**No new authorization primitive was needed**: `CanAccessCase`/
`CanAccessDocument` (System 4) already expressed exactly "RBAC permission
AND resource relationship" — System 6 just supplies a different `Action`
constant (`ActionDocumentUpload`/`ActionDocumentDownload`, both already
defined in `internal/authz/action.go` since System 4). This is the
"stable service/repository interfaces future systems can use" master
prompt §47 asked System 5 to leave behind, now exercised by a second
system.

**sqlc**: one existing query changed (`CreateDocument` now takes an
explicit `id` parameter, generated via `uuid.New()` in Go, rather than
relying on the column's `DEFAULT gen_random_uuid()`) — necessary because
the object key must be known before the row is inserted (the file is
streamed to MinIO first). No migration: `documents`' schema, indexes, and
RLS policies (System 2) were already sufficient.

**What's genuinely new here**: `pkg/hash` (previously a TODO stub) now
implements streaming/one-shot SHA-256; `internal/service.DocumentService`
and `internal/handlers/document/{upload,download}.go` are the first live
document business logic; `config.DocumentsConfig` and
`internal/handlers/document/dto.go`'s `writeMultipartReadError` (mapping
both the coarse body-size guard and the fine-grained streaming guard to
the identical `413`) are new. `internal/service.DocumentSummary` (added
in System 5 for case-detail's embedded document references) gained
`case_id`/`description`/`sha256_hash` fields and is now also System 6's
standalone upload-response shape — one DTO, two call sites, not a
duplicate.

**What's deliberately not here**: hash verification/tamper detection and
compliance certificates (System 7, below), redaction/derivative
generation (a future redaction system), the audit hash chain (System 8),
and document share (`internal/handlers/document/{redact,share}.go` remain
TODO stubs). Full design: [docs/SECURITY.md](./docs/SECURITY.md)'s
"Document Management" section and [docs/STORAGE.md](./docs/STORAGE.md);
tests: `backend/pkg/hash/sha256_test.go`,
`backend/internal/service/document_service_test.go` (pure unit tests:
filename sanitization, object-key generation, streaming size guard, MIME
sniffing), `backend/internal/service/document_service_integration_test.go`,
`backend/internal/httpserver/document_flow_integration_test.go`.

## System 7 — Evidence Verification, Tamper Detection & Compliance Certificates (Implemented)

```text
internal/handlers/document.Verify (POST, no body)
    |
    v
internal/service.DocumentService.VerifyDocument
    |
    +--> authz.Service.CanAccessDocument(ctx, user, docID, ActionDocumentVerify)
    +--> repository.WithTx: GetDocumentByID (under RLS) — documents.sha256_hash is canonical
    +--> recomputeDocumentHash(ctx, storage, doc)  — stream MinIO object -> pkg/hash, never io.ReadAll
    +--> bytes.Equal(computed, doc.Sha256Hash)
    |      match    -> VERIFIED
    |      mismatch -> INTEGRITY_FAILURE (doc.Sha256Hash is NEVER rewritten)
    +--> reconcileTamperStatus — documents.status -> TAMPERED/ACTIVE, only if it must change
    +--> audit.Recorder — DOCUMENT_VERIFIED / DOCUMENT_INTEGRITY_FAILURE

internal/handlers/document.Certificate (GET — retrieval, or generation on demand)
    |
    v
internal/service.CertificateService.GetOrCreateCertificate
    |
    +--> authz.Service.CanAccessDocument(ctx, user, docID, ActionCertificateRead)
    +--> repository.WithTx: GetDocumentByID (under RLS)
    +--> fetchCertificate(docID, doc.Sha256Hash) — existing cert for the CURRENT hash?
    |      found -> return it (200)
    +--> not found: authz.Service.CanAccessDocument(..., ActionCertificateCreate)
    |      denied -> 404 (indistinguishable from "not generated yet")
    +--> generateCertificate:
    |      +--> recomputeDocumentHash (SAME function VerifyDocument uses — package-level,
    |      |      not a DocumentService method, so neither service depends on the other)
    |      +--> mismatch -> reconcileTamperStatus + audit DOCUMENT_INTEGRITY_FAILURE, 409, no certificate
    |      +--> match -> canonicalCertificatePayload (fixed field order, never arbitrary JSON)
    |             +--> pkg/crypto.SignECDSA (ECDSA P-256, signing key never hardcoded/logged/returned)
    |             +--> repository.WithTx: CreateCertificate — INSERT ... ON CONFLICT (document_id,
    |             |      document_hash) DO NOTHING (000003_certificate_integrity.up.sql); a losing
    |             |      concurrent request fetches and returns the winning row instead of erroring
    |             +--> audit.Recorder — CERTIFICATE_CREATED
```

`app.App` gained a `CertificateService *service.CertificateService` field
(constructed in `app.New`, sharing `AuthzService`/`audit.Recorder`/
`Storage` with `DocumentService`, plus a new
`config.CertificateConfig.SigningKeyPEM` — `CERTIFICATE_SIGNING_KEY`,
optional; an ephemeral, process-lifetime ECDSA key is generated instead
when unset — see that field's doc comment). Routes registered in
`internal/httpserver/router.go`:

```text
POST /api/v1/documents/:id/verify       Auth + RequireDocumentAccess(document:verify, "id")
GET  /api/v1/documents/:id/certificate  Auth + RequireDocumentAccess(certificate:read, "id")
```

**No new authorization primitive was needed**: `CanAccessDocument`
(System 4) already expressed exactly "RBAC permission AND resource
relationship" — System 7 supplies two more `Action` constants
(`ActionDocumentVerify`/`ActionCertificateRead`/`ActionCertificateCreate`,
already defined in `internal/authz/action.go` and seeded in
`db/seed/001_reference_data.sql` since System 4/6's authorization
groundwork). Certificate generation is the one route with a *second*,
service-layer-only authorization check beyond what the route's own
middleware enforces (`certificate:create`, ADMIN-only per seed data) —
see `docs/SECURITY.md`'s "Document Verification & Compliance
Certificates" for why that split lives in `CertificateService`, not a
second route.

**sqlc/migration**: `000003_certificate_integrity.up.sql` adds one
constraint — `UNIQUE (document_id, document_hash)` on
`compliance_certificates` — backing `CreateCertificate`'s new
`ON CONFLICT DO NOTHING` clause. Two new queries
(`GetCertificateByDocumentAndHash`, `GetCertificateByDocumentID`)
support fetching the winning row after a losing concurrent insert and
certificate retrieval by document. No other schema change: `documents`'
`status` column already reserved the `TAMPERED` value (System 2's own
comment), and `compliance_certificates`' shape (System 2) already matched
what System 7 needed.

**What's genuinely new here**: `pkg/crypto/ecdsa_sign.go` (previously a
TODO stub) now implements ECDSA key generation/parsing/signing/
verification; `internal/service.CertificateService` and
`internal/handlers/document/{verify,certificate}.go` are the first live
verification/certificate business logic;
`internal/service.recomputeDocumentHash`/`reconcileTamperStatus` are
package-level (not methods) specifically so `DocumentService` and
`CertificateService` share identical hashing/tamper-status logic without
either depending on the other.

**What's deliberately not here**: the audit hash chain (System 8, per the
numbering already established throughout Systems 2-6's code and the
applied migrations themselves), redaction/derivative generation (a future
redaction system), document share, and a public certificate-verification
HTTP endpoint (`CertificateService.VerifyCertificateIntegrity` exists and
is tested, but no route exposes it — see `docs/SECURITY.md`). Full
design: [docs/SECURITY.md](./docs/SECURITY.md)'s "Document Verification &
Compliance Certificates" section and [docs/STORAGE.md](./docs/STORAGE.md)'s
"Document Verification Pipeline"; tests:
`backend/pkg/crypto/ecdsa_sign_test.go`,
`backend/internal/service/document_verify_integration_test.go`,
`backend/internal/service/certificate_service_integration_test.go`,
`backend/internal/httpserver/document_verify_certificate_flow_integration_test.go`.

## Document Redaction (Implemented)

```text
internal/handlers/document.Redact (POST, JSON body: reason + regions)
    |
    v
internal/service.DocumentService.RedactDocument
    |
    +--> authz.Service.CanAccessDocument(ctx, user, sourceID, ActionDocumentRedact)
    |      (document:redact — ADMIN-only per seed data; no new grant added)
    +--> validateRedactionReason / validateRedactionRegions — request shape only
    +--> repository.WithTx: GetDocumentByID (under RLS) — the SOURCE row, read-only
    +--> supportedRedactionFormats[doc.MimeType] — else 422 (image/png, image/jpeg ONLY)
    +--> readAllLimited(storage, doc.StorageObjectKey, maxUploadSize)
    +--> recomputeDocumentHash-equivalent (sha256Sum) + reconcileTamperStatus
    |      mismatch -> audit DOCUMENT_INTEGRITY_FAILURE, 409, no derivative created
    +--> image.Decode -> validate regions against REAL bounds -> 400 if out of bounds
    +--> applyRedactions — draw.Draw(..., draw.Src) destructive pixel replace, in memory
    +--> re-encode (png.Encode / jpeg.Encode) -> sha256Sum -> H2 (server-computed only)
    +--> documentObjectKey(caseID, NEW derivativeID) -> storage.Put (new object, new key)
    +--> repository.WithTx:
    |      CreateDocument (parent_document_id = source.ID, Sha256Hash = H2)
    |      CreateRedaction (source_document_id, result_document_id, region_data, reason)
    |      (transaction failure -> cleanupOrphan, same pattern UploadDocument uses)
    +--> audit.Recorder — DOCUMENT_REDACTED (both hashes, region count, reason)
```

The source document (`A`) is only ever read in this flow — never
UPDATEd, never re-hashed-and-written, never deleted. The derivative (`B`)
is a completely ordinary `documents` row from the perspective of every
other System 7 route: `POST /documents/{B}/verify` and
`GET /documents/{B}/certificate` work unmodified, with zero code changes
to either `DocumentService.VerifyDocument` or `CertificateService` — the
independence those two already guaranteed between any two documents'
hashes/certificates is exactly what makes a redacted derivative safe to
introduce without touching them.

Routes registered in `internal/httpserver/router.go`:

```text
POST /api/v1/documents/:id/redact  Auth + RequireDocumentAccess(document:redact, "id") + jsonBodyLimit
```

**No new authorization primitive was needed**: `CanAccessDocument`
(System 4) already expressed exactly "RBAC permission AND resource
relationship"; this system reuses the existing `ActionDocumentRedact`
constant and `document:redact` seed row (both already present since
System 2/4, unused by any route until now) rather than adding either.
`document:redact` remains ADMIN-only — `backend/tests/rbac_test.go`'s
`TestRBAC_PolicePermissions` already asserted this, and this system does
not touch `role_permissions`.

**sqlc/migration**: none. `redactions` (table, RLS policies, sqlc
queries `CreateRedaction`/`GetRedactionByResultDocument`/
`ListRedactionsBySourceDocument`) and `documents.parent_document_id`
were already fully in place since System 2 — this system is the first to
actually call them. The only schema-adjacent change is a new
`DocumentSummary.parent_document_id` field (Go/JSON only, no migration)
so API responses that already return document metadata now also surface
lineage.

**What's genuinely new here**: `internal/service/document_redact.go`
(image decode/mask/re-encode pipeline, using only Go's standard library
`image`/`image/draw`/`image/png`/`image/jpeg` — no new dependency beyond
`TECH_STACK.md`'s existing stack) and
`internal/handlers/document/redact.go` (previously a TODO stub) are the
first live redaction business logic; `utils.ErrUnprocessableEntity`
(422) is a new, small addition to the shared `AppError` helpers for
"well-formed and authorized, but no safe implementation exists for this
resource" — distinct from both 400 (malformed request) and 409 (state
conflict).

**What's deliberately not here**: redaction of any non-raster-image
format (PDF included) — refused with 422 rather than faked, since no
approved library in this project's stack can safely strip underlying
content from those formats yet; the audit hash chain (unchanged); document
share; expanding `document:redact` beyond ADMIN. Full design:
[docs/SECURITY.md](./docs/SECURITY.md)'s "Document Redaction" section and
[docs/STORAGE.md](./docs/STORAGE.md); tests:
`backend/internal/service/document_redact_test.go`,
`backend/internal/service/document_redact_integration_test.go`,
`backend/internal/httpserver/document_redact_flow_integration_test.go`.

## Document Sharing (Implemented)

```text
internal/handlers/document.Share (POST, JSON body: user_id/permission/expires_at/reason)
    |
    v
internal/service.ShareService.CreateShare
    |
    +--> authz.Service.CanAccessDocument(ctx, user, sourceID, ActionDocumentShare)
    |      (the IDENTICAL RBAC+ABAC check every document route already uses)
    +--> validate permission (VIEW|VERIFY) / expires_at (future or nil) / not self-share
    +--> repository.WithTx: GetDocumentByID (under RLS) — confirms case_id, re-authorizes
    +--> validateRecipient — GetUserByID, must exist AND status = active
    +--> repository.WithTx: CreateDocumentShare
    |      (document_shares_active_unique violation -> 409, one active share per pair)
    +--> audit.Recorder — DOCUMENT_SHARED

internal/authz.Service.CanAccessDocument (EVERY document/certificate route, unchanged call site)
    |
    +--> HasPermission(action)                          <- RBAC, unchanged, checked FIRST
    +--> GetDocumentByID (RLS now ALSO allows a valid share — see below)
    +--> isAdmin -> allow
    +--> loadCaseRelationship -> isOwner/isMember -> allow
    +--> shareGrantsAccess(user, documentID, action)     <- NEW fallback, ABAC path 2
    |      (only for Read/Download/CertificateRead/Verify — never Redact/Share/
    |       CertificateCreate, which are simply absent from its action maps)
    +--> deny
```

The genuinely new authorization surface is exactly one method,
`authz.Service.shareGrantsAccess` (`internal/authz/share_policy.go`),
consulted only once RBAC has already passed AND the existing case-
relationship check has already failed — a second, narrower fallback, never
a replacement for either. No new authorization engine; no route's
existing middleware/handler changed.

Routes registered in `internal/httpserver/router.go`:

```text
POST /api/v1/documents/:id/share                    Auth + RequireDocumentAccess(document:share, "id") + jsonBodyLimit
GET  /api/v1/documents/:id/shares                   Auth + RequireDocumentAccess(document:share, "id")
POST /api/v1/documents/:id/shares/:shareId/revoke   Auth + RequireDocumentAccess(document:share, "id")
GET  /api/v1/shared/documents                       Auth only ("Shared With Me")
GET  /api/v1/users/search                           Auth only (recipient picker)
```

**No new RBAC permission was needed**: `document:share` was already
seeded (System 2/4) and already granted to POLICE/LAWYER/ADMIN in the
existing `role_permissions` matrix — unused by any route until now. This
system does not touch `role_permissions`.

**sqlc/migration**: `000004_document_sharing.up.sql` adds `document_shares`
(permission/status/expires_at/revoked_at/revoked_by_user_id, a partial
`UNIQUE (document_id, shared_with_user_id) WHERE status = 'ACTIVE'` index
doubling as both the hot-path lookup index and the duplicate-active-share
guard), its own RLS policies, and — the one genuinely novel piece —
`has_active_document_share`, a `SECURITY DEFINER` function that exists
solely to break an RLS<->RLS circular dependency: `documents_select`'s
new delegated-access branch needs to check `document_shares`, but
`document_shares_select` itself needs to check `documents` (for its own
case-member visibility rule), and PostgreSQL refuses to evaluate that
cycle (SQLSTATE 42P17, "infinite recursion detected in policy"). The
function, owned by the migrator role (a superuser, hence RLS-exempt
regardless of `FORCE`), queries `document_shares` without re-entering its
RLS, breaking the cycle cleanly — see
[docs/SECURITY.md](./docs/SECURITY.md)'s "Document Sharing" for the full
incident writeup and [docs/DATABASE_SCHEMA.md](./docs/DATABASE_SCHEMA.md)
for the schema-level summary. Two new queries
(`GetActiveShareForDocumentAndUser`, `ListSharedWithMe`) support the
authorization hot path and the "Shared With Me" listing respectively.

**What's genuinely new here**: `internal/service/document_share.go`
(`ShareService` — create/list/revoke/"Shared With Me"/recipient search),
`internal/authz/share_policy.go` (the delegated-access check and its
strict VIEW/VERIFY -> Action maps), `internal/handlers/document/share*.go`,
the new `internal/handlers/shared` package, and
`internal/handlers/user.Search` (deliberately NOT gated by the admin-only
`user:read` permission — a narrower, safer capability any authenticated
user needs to find a share recipient). `DocumentSummary.parent_document_id`
(already added by the redaction system) is reused as-is for "Shared With
Me"'s document metadata — no new document-summary shape was needed.

**What's deliberately not here**: a third `DOWNLOAD` permission tier
(`VIEW` already covers it — this application has no distinct
metadata-only view separate from download); public/anonymous links or
link-plus-password access (strictly authenticated user-to-user, every
route behind `middleware.Auth`); an "act as user" mechanism for ADMIN;
rate limiting (this codebase has none for any route yet); the audit hash
chain. Full design: [docs/SECURITY.md](./docs/SECURITY.md)'s "Document
Sharing" section; tests:
`backend/internal/authz/share_policy_test.go`,
`backend/internal/service/document_share_test.go`,
`backend/internal/service/document_share_integration_test.go`,
`backend/internal/httpserver/document_share_flow_integration_test.go`.

## Request Flow (Intended, Later Systems)

```text
Frontend
    |
    | HTTPS / REST / SSE
    v
Gin API
    |
    +--> Authentication Middleware
    |
    +--> Authorization Middleware
    |       |
    |       +--> RBAC
    |       +--> ABAC
    |
    +--> Validation Middleware
    |
    +--> Audit Middleware
    |
    v
Handlers
    |
    v
Services
    |
    +-------------------+
    |                   |
    v                   v
Repository          Storage
(sqlc)              (MinIO)
    |
    v
PostgreSQL
    |
    +--> RLS
    +--> JSONB
    +--> Cases
    +--> Documents
    +--> Users
    +--> Roles
    +--> Audit Log
    +--> Certificates

Redis
    |
    v
Asynq Workers
    |
    +--> Audit Verification
    +--> Certificate Jobs
    +--> Background Processing
```

## Layering Principles

The codebase enforces strict separation of concerns:

```text
Handlers
   ↓
Services
   ↓
Repositories
   ↓
Database

Services
   ↓
Storage

Services
   ↓
Jobs

Jobs
   ↓
Redis / Asynq

Services
   ↓
Realtime / SSE
```

- **Handlers** parse/validate HTTP input and delegate to services. They
  contain no business logic.
- **Services** contain business logic and orchestrate repositories, storage,
  jobs, and realtime notifications.
- **Repositories** are the only layer that talks to the database, via
  sqlc-generated code.
- **Storage** abstracts object storage (MinIO today, local disk as an
  alternate implementation) behind a common interface.
- **Jobs** encapsulate asynchronous work dispatched to Redis/Asynq workers.
- **Realtime** manages SSE connections and event broadcasting.

## Core Domains

- **Auth** (implemented — System 3) — JWT issuance/validation, refresh
  tokens, password hashing.
- **RBAC/ABAC** (implemented — System 4) — Role- and attribute-based
  authorization (`internal/authz`), enforced above the database and
  reinforced by PostgreSQL RLS. Cases, documents, and admin now guard their
  routes with it; audit's routes remain a later system's scope.
- **Cases** (implemented — System 5) — Case CRUD, role-scoped listing,
  status lifecycle, involved parties, case-user membership
  (`internal/service.CaseService`, `internal/handlers/case`).
- **Documents** (implemented — Systems 6/7) — Evidence document ingestion
  (streaming SHA-256 + MinIO storage + PostgreSQL metadata), authorized
  retrieval, integrity *verification*/tamper detection, and compliance
  certificate generation/retrieval
  (`internal/service.{DocumentService,CertificateService}`,
  `internal/handlers/document`). Redaction lineage and document sharing
  remain later systems.
- **Admin / User Management** (implemented — System 8) — Admin-only user
  CRUD, role assignment, account status, and password reset
  (`internal/service.UserService`, `internal/handlers/user`), plus a
  one-time initial-admin bootstrap (`internal/bootstrap`). Every other
  user is created by an existing ADMIN through `POST /admin/users` — see
  [docs/API_ENDPOINTS.md](./docs/API_ENDPOINTS.md)'s Admin section.
- **Audit Chain** (implemented) — Immutable, hash-chained audit log of
  security-sensitive actions (`internal/audit/{writer,chain,verifier}.go`,
  `internal/service.AuditService`, `internal/handlers/audit`). Every
  existing `audit.Recorder` call site across every other system now
  durably persists through `audit.ChainWriter`'s SHA-256 hash chain,
  rather than the operational-log-only `audit.SlogRecorder` placeholder
  used until this system landed — see
  [docs/AUDIT_CHAIN.md](./docs/AUDIT_CHAIN.md) for the full design.
- **Audit Chain Verification & Integrity Dashboard** (implemented) —
  Asynchronous audit-chain verification (`internal/jobs`, `internal/
  realtime`, `internal/service.AuditService.RunVerification`) so a chain
  of any size verifies via a background job rather than one long-running
  HTTP request: `POST /audit/verify-chain` returns `202` and dispatches an
  Asynq task; progress/result are tracked in the durable
  `audit_verifications` table and streamed live over SSE
  (`GET /audit/verify-chain/:id/events`). Reuses System 10's hash/
  canonicalization/verification logic completely, unchanged. See
  [docs/AUDIT_CHAIN.md](./docs/AUDIT_CHAIN.md)'s "Asynchronous
  Verification & Integrity Dashboard".
- **Asynchronous Processing & Background Jobs** (implemented) — System 12
  generalizes System 11's Asynq integration into reusable infrastructure
  (`internal/jobs`): queue priority (`QueueCritical`/`QueueDefault`),
  `LoggingMiddleware`, `FailureCategory`/`Permanent`/`CategoryOf` retry
  classification, and `DeterministicTaskID` (a traceable `job_id`, now
  returned alongside `verification_id`, doubling as a second, Asynq-level
  idempotency guard). `AUDIT_CHAIN_VERIFY` is refactored onto it with no
  behavior change; no other Systems 1-11 operation was moved to
  background processing — see
  [docs/BACKGROUND_JOBS.md](./docs/BACKGROUND_JOBS.md).
- **Crypto** — SHA-256 integrity hashing (implemented — System 6/7) and
  ECDSA compliance-certificate signing (implemented — System 7,
  `pkg/crypto`); AES-256 encryption and RSA signing remain future.
- **Storage** — MinIO-backed object storage behind a provider-agnostic
  interface.
- **Jobs** (implemented) — System 12's reusable Redis/Asynq background-
  processing architecture (`internal/jobs`): named priority queues
  (`critical`/`default`), a shared structured-logging middleware, a
  TRANSIENT/PERMANENT/SECURITY/INTEGRITY error-classification vocabulary,
  and deterministic, traceable job IDs — used by System 11's audit-chain
  verification (its only consumer today; Systems 6-8's hashing,
  certificate generation, and redaction were each evaluated and
  deliberately kept synchronous — see
  [docs/BACKGROUND_JOBS.md](./docs/BACKGROUND_JOBS.md)'s "Task Types" for
  why). The worker runs embedded in the same process as the HTTP server
  (`cmd/server/main.go`), not a separate deployment unit.
- **Realtime** (implemented) — SSE-based progress streaming
  (`internal/realtime`), so far powering audit-chain verification
  progress only, via an in-process broadcaster (not Redis pub/sub — see
  docs/AUDIT_CHAIN.md for why).

## Data Model Overview

Implemented — see [docs/DATABASE_SCHEMA.md](./docs/DATABASE_SCHEMA.md) for
the full ER diagram, every table's purpose, and the design decisions
behind each (UUID/timestamp/hash representation, why there's no
`agencies` table, why controlled-vocabulary columns use `CHECK` instead of
native `ENUM`, and more).

## Security Principles (Architectural Intentions)

The eventual system will enforce:

1. JWT authentication
2. RBAC
3. ABAC
4. PostgreSQL Row-Level Security
5. SHA-256 document integrity
6. AES-256 encryption
7. Immutable, append-only audit logs
8. Hash-chained audit entries
9. Transactional / concurrency-safe audit writing
10. TLS in transit
11. Secure refresh-token handling
12. Audit logging of all security-sensitive actions

**1** (JWT) and **11** (refresh-token rotation/revocation) were
implemented in System 3; **4** (RLS) was implemented in System 2, enforced
with policies and fail-closed behavior verified by integration tests;
**2** (RBAC) and **3** (ABAC) are implemented in System 4
(`internal/authz`), composed with RLS as defense-in-depth rather than
replacing it. **7**/**8**/**9** (immutable append-only audit logs,
hash-chained entries, transactional/concurrency-safe writing) are now
fully implemented — see [docs/AUDIT_CHAIN.md](./docs/AUDIT_CHAIN.md).
**12** (audit logging of all security-sensitive actions) now durably
records through that same hash chain, not just the operational log — see
SECURITY.md. **5** (SHA-256 document integrity) is complete end-to-end:
System 6 *computes and persists* the initial hash at ingestion
(`pkg/hash`, `documents.sha256_hash`), and System 7 *recomputes and
compares* a stored object's current hash against it to detect tampering
(`DocumentService.VerifyDocument`), never rewriting the canonical value on
a mismatch. AES-256 (**6**) and TLS (**10**) remain unimplemented. See
[docs/SECURITY.md](./docs/SECURITY.md),
[docs/AUDIT_CHAIN.md](./docs/AUDIT_CHAIN.md), and
[docs/DATABASE_SCHEMA.md](./docs/DATABASE_SCHEMA.md) for what each
currently covers.
