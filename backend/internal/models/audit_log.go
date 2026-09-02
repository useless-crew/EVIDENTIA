package models

import "evidentia/backend/db/generated"

// AuditLogEntry — see the audit_log table comment in the migration for the
// hash-chain invariants this schema enforces. Hash-chain computation
// itself belongs to a later system; this is storage only.
type AuditLogEntry = generated.AuditLog
