# Storage

## Purpose

TODO: Document the object storage architecture for evidence documents.

## Storage Interface

TODO: Document the storage abstraction that allows swapping between MinIO
and local disk — see `backend/internal/storage/storage.go`.

```text
Storage Interface
      │
      ├── MinIO
      └── Local Disk
```

## MinIO

TODO: Document bucket layout, object naming, and access-key management —
see `backend/internal/storage/minio_client.go`.

## Local Disk (Development/Fallback)

TODO: Document local storage layout — see
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
