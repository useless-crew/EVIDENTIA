# Storage

## Purpose

Document the object storage architecture for evidence documents: the
`Storage` interface and its two implementations (System 1/2), the
System 6 document-ingestion/retrieval pipeline that uses them, and
System 7's verification/tamper-detection pipeline built on top of it
(compliance certificates reuse the same recompute-and-compare read path —
see [SECURITY.md](./SECURITY.md)'s "Document Verification & Compliance
Certificates" for the certificate-specific design). Redaction/derivative
generation is now implemented on top of exactly this storage layout (a
new document row + a new object, original never overwritten) — see
[SECURITY.md](./SECURITY.md)'s "Document Redaction" for that system's own
design; this document's storage-mechanics description below still
applies unchanged to a redacted derivative's object.

## Storage Interface (Implemented)

`backend/internal/storage/storage.go` defines `Storage`: `Put`, `Get`,
`Delete`, `Exists`, `HealthCheck`, each `context`-aware. `Get`/`Delete`
report a shared `ErrNotFound` sentinel regardless of backend, so callers
can use `errors.Is` without caring which implementation is active.

```text
Storage Interface
      │
      ├── MinIOStorage  (production — internal/storage/minio_client.go)
      └── LocalStorage  (dev/tests  — internal/storage/local_storage.go)
```

## MinIO (Implemented)

`NewMinIO` connects using `MINIO_*` config, verifies the configured bucket
exists (creating it if this is a fresh instance — idempotent), and fails
startup if the endpoint is unreachable. See
`backend/internal/storage/minio_client.go`. Bucket layout/object naming
conventions are defined by System 6 — see "Document Upload Pipeline" below.

**MinIO is never a public file server.** No bucket policy grants public
read; the application is the sole authorization gateway. Documents are
served via authenticated, server-side streaming
(`DocumentService.DownloadDocument` → `Storage.Get` → HTTP response) —
never a pre-signed or otherwise directly-reachable URL. `docker-compose.yml`
exposes MinIO's ports (`9000`/`9001`) to the host only for local
development/admin-console access; the backend container reaches it over
the internal Docker network (`MINIO_ENDPOINT=minio:9000`), not through
those published ports.

## Local Disk (Development/Fallback) (Implemented)

`NewLocal` roots a `Storage` implementation at a given directory, rejecting
keys that would escape it (absolute paths, `..`). Used today as the fast,
hermetic backend for `internal/storage`'s own tests — not selected by
configuration for production. See
`backend/internal/storage/local_storage.go`.

## Document Upload Pipeline (Implemented — System 6)

```text
Client (multipart/form-data)
 ↓
Authenticate (System 3) + Authorize case (System 4: document:upload + CanAccessCase)
 ↓
Validate document_type / description
 ↓
Sanitize filename, generate document UUID, build object key
 ↓
Stream file: SHA-256 (pkg/hash) + MinIO Put, in one pass
 ↓
Persist metadata (PostgreSQL, transaction-local RLS identity)
 ↓
Audit event (DOCUMENT_UPLOADED)
 ↓
Response (never the raw storage_bucket/storage_object_key)
```

`internal/service.DocumentService.UploadDocument` (`internal/handlers/
document/upload.go` for the HTTP-facing multipart parsing) is the single
implementation of this pipeline. Key properties:

- **True streaming, not buffer-then-forward**: the handler reads the
  multipart body via `http.Request.MultipartReader` (never
  `c.Request.ParseMultipartForm`/`c.FormFile`, both of which buffer a part
  to memory or a temp file before a handler ever sees it). The file part
  is wrapped in `io.TeeReader` so the same bytes are simultaneously hashed
  (`pkg/hash.New()`) and written to `Storage.Put` — read from the network
  exactly once, memory usage independent of file size.
- **Streaming size guard**: a `limitedReader` aborts the stream the moment
  the running byte count would exceed `DocumentsConfig.MaxUploadSize`,
  before an oversized file can be fully written to storage or fully
  hashed. A second, coarser guard (`middleware.BodyLimit` on the whole
  request) sits in front of it; both map to the same `413` response (see
  `docs/API_ENDPOINTS.md`).
- **Content-based MIME detection**: `http.DetectContentType` inspects the
  first 512 bytes of the actual stream — the client's declared
  `Content-Type` on the file part is never trusted or persisted.
- **Server-generated object key**: `cases/{case_id}/documents/{document_id}/original`,
  where both IDs are server-resolved (the case ID from the authorized
  route parameter, the document ID freshly generated via `uuid.New()`
  before the file is streamed) — never a client-supplied path fragment.
  There is no filename in the key at all, so filename sanitization
  (control characters, path separators under both `/` and `\`
  conventions, length) only ever affects display metadata
  (`documents.filename`, and the `Content-Disposition` header on
  download), never storage addressing.
- **Atomicity across two non-transactional systems**: PostgreSQL and
  MinIO do not share a transaction. The file is streamed to MinIO
  *before* the PostgreSQL insert (so the metadata row that finally commits
  always refers to a real, already-written object — never the reverse). If
  the PostgreSQL insert then fails for any reason, `DocumentService`
  best-effort deletes the just-written object (`Storage.Delete` on a
  now-orphaned key); if that deletion *also* fails, the orphaned-object
  condition is logged operationally at ERROR level with the case/document
  ID and object key for manual reconciliation — the upload is still
  reported as failed to the client either way, never a false "success".
- **Original bytes are immutable**: there is no `PUT /documents/:id/file`
  and no code path that overwrites an existing object. Redaction (now
  implemented — see [SECURITY.md](./SECURITY.md)'s "Document Redaction")
  produces a derivative by creating a new document row and a new object;
  it never modifies the original, and could not have been made to.

## Document Download Pipeline (Implemented — System 6)

```text
Client
 ↓
Authenticate (System 3) + Authorize (System 4: document:download + CanAccessDocument)
 ↓
Resolve document row under RLS (PostgreSQL)
 ↓
Retrieve object from MinIO  ← only after the above succeeds, never before
 ↓
Audit event (DOCUMENT_DOWNLOADED)
 ↓
Stream object → HTTP response (gin's DataFromReader)
```

`DocumentService.DownloadDocument` never calls `Storage.Get` until
`authz.Service.CanAccessDocument` has already allowed the request AND the
document row has been loaded under the caller's own transaction-local RLS
identity — object storage has no authorization concept of its own (RLS
protects PostgreSQL rows, not MinIO objects), so the database decision
must always come first. The audit event is recorded once the object
stream is confirmed retrievable, not after the client finishes reading
the (potentially large) response body.

A document row whose object is missing from storage (deleted out-of-band,
storage outage, ...) is treated as a genuine inconsistency: logged
operationally with the object key, and reported to the client as a
generic `503 SERVICE_UNAVAILABLE` — never a raw MinIO driver error,
never silent data loss.

The response is always `Content-Disposition: attachment` (never inline)
plus `X-Content-Type-Options: nosniff`, so a browser never executes or
renders evidence content by default — this system is storage/retrieval
infrastructure, not a document viewer or execution engine.

## Document Verification Pipeline (Implemented — System 7)

```text
Client (no body)
 ↓
Authenticate (System 3) + Authorize (System 4: document:verify + CanAccessDocument)
 ↓
Resolve document row under RLS (PostgreSQL) — documents.sha256_hash is canonical
 ↓
Retrieve object from MinIO  ← only after the above succeeds, never before
 ↓
Stream object → SHA-256 (pkg/hash), never io.ReadAll
 ↓
bytes.Equal(computed_hash, documents.sha256_hash)
 ↓
Reconcile documents.status (TAMPERED/ACTIVE) — sha256_hash itself is NEVER written
 ↓
Audit event (DOCUMENT_VERIFIED / DOCUMENT_INTEGRITY_FAILURE)
 ↓
Response: both hashes, status VERIFIED or INTEGRITY_FAILURE (both are 200)
```

`internal/service.DocumentService.VerifyDocument` reuses this pipeline's
"database before storage, always" ordering and streaming discipline
exactly as System 6's download path established them — see
[SECURITY.md](./SECURITY.md)'s "Document Verification & Compliance
Certificates" for the full design, including why a storage error (`503`)
and a detected mismatch (`INTEGRITY_FAILURE`, `200`) are categorically
different outcomes that are never conflated, and why
`documents.sha256_hash` is never rewritten regardless of the result.

`internal/service.CertificateService` reads through this same MinIO path
(the shared, package-level `recomputeDocumentHash` function — not a
second implementation) when generating a compliance certificate: it
re-verifies the document's hash immediately before signing and refuses
(`409`) on any mismatch, so a tampered document can never receive a valid
certificate. Certificates never store or re-derive object bytes — only
the resulting hash, already computed by this pipeline, is persisted.

## Future Systems (Not Implemented Here)

- The audit hash chain itself (`audit_log.hash`/`prev_hash`); System 6/7's
  `DOCUMENT_*`/`CERTIFICATE_*` events, and redaction's own
  `DOCUMENT_REDACTED`/`DOCUMENT_INTEGRITY_FAILURE` events, go through the
  same `audit.Recorder` interface System 3/4/5 already use (today: the
  operational log only).
