package tests

// Audit-chain construction, writing, and verification (including
// chain-break/tamper detection and the mandatory concurrency test) are
// implemented and tested in:
//
//   - backend/internal/audit/chain_test.go       — canonicalization/hash
//     determinism (pure, no database)
//   - backend/internal/audit/verifier_test.go     — VerifyBatch chain-
//     break/tamper/fork/resume detection (pure, no database)
//   - backend/internal/service/audit_service_integration_test.go —
//     ChainWriter genesis/sequential-chaining, the concurrent-writers-
//     cannot-fork test, AuditService.List RBAC/RLS/filtering, and
//     AuditService.VerifyChain (fresh chain, privileged-connection
//     tampering, deleted-entry detection, empty chain, resumable
//     pagination, cancelled-context rollback safety)
//
// This package (backend/tests) keeps the schema/privilege-level audit
// tests only — see db_audit_privileges_test.go (RLS, INSERT/UPDATE/
// DELETE grants, BYPASSRLS, genesis/predecessor uniqueness at the
// database level, independent of any Go hashing logic).
