package tests

// SHA-256 unit tests (known vectors, streaming-vs-buffered equivalence,
// content-only hashing) live with the algorithm itself:
// backend/pkg/hash/sha256_test.go. System 6's document-upload integration
// tests (backend/internal/service/document_service_integration_test.go)
// verify the hash is computed and persisted correctly end to end (MinIO +
// PostgreSQL).
//
// TODO (System 7): hash *verification*/tamper-detection integration
// tests belong here once that subsystem exists — this package
// deliberately does not duplicate that scope now.
