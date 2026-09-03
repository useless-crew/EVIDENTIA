# Audit Chain

## Purpose

TODO: Document the immutable, hash-chained audit-log architecture.

**Note (System 7):** `internal/service.DocumentService.VerifyDocument`
and `internal/service.CertificateService` record semantic events
(`DOCUMENT_VERIFIED`, `DOCUMENT_INTEGRITY_FAILURE`, `CERTIFICATE_CREATED`)
through the existing `internal/audit.Recorder` interface — the same one
every prior system uses, today writing to the operational log only. This
is future input to the hash chain this document describes; System 7 does
not compute or verify hashes/chain linkage itself, and `audit_log`'s
storage invariants (genesis/uniqueness constraints — see
[DATABASE_SCHEMA.md](./DATABASE_SCHEMA.md)) are unchanged. See
[SECURITY.md](./SECURITY.md)'s "Document Verification & Compliance
Certificates" for what each event records and why.

## Entry Structure

TODO: Document the fields of an audit-log entry (actor, action, resource,
timestamp, metadata, entry hash, previous hash).

## Hash Construction

TODO: Document how each entry's hash is derived from its content and the
previous entry's hash (chain linkage).

## Writing

TODO: Document transactional/concurrency-safe append semantics — see
`backend/internal/audit/writer.go`.

## Verification

TODO: Document full-chain verification, break detection, and progress
reporting — see `backend/internal/audit/verifier.go`.

## Relationship to Async Jobs

TODO: Document how long-running chain verification is dispatched via
Redis/Asynq and how progress is streamed via SSE — see
`backend/internal/jobs/audit_verification.go` and
`backend/internal/realtime/`.
