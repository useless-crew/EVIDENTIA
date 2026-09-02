# Audit Chain

## Purpose

TODO: Document the immutable, hash-chained audit-log architecture.

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
