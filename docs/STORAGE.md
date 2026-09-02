# Storage

## Purpose

TODO: Document the object storage architecture for evidence documents.
The interface and its two implementations (below) are implemented; the
document-integrity pipeline that will use them is not.

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
conventions are not yet defined — that's for the document system.

## Local Disk (Development/Fallback) (Implemented)

`NewLocal` roots a `Storage` implementation at a given directory, rejecting
keys that would escape it (absolute paths, `..`). Used today as the fast,
hermetic backend for `internal/storage`'s own tests — not selected by
configuration for production. See
`backend/internal/storage/local_storage.go`.

## Document Integrity Pipeline

```text
Upload
 ↓
SHA-256
 ↓
MinIO
 ↓
PostgreSQL metadata
```

TODO: Document the full upload → hash → store → record pipeline.
