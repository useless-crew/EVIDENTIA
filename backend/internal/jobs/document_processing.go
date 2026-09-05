package jobs

// System 12 (Asynchronous Processing & Background Jobs) evaluated
// existing document-processing operations (upload hashing, redaction) as
// candidate task types and deliberately did NOT move either onto this
// package's infrastructure.
//
// Upload hashing (internal/service.DocumentService.streamToStorage)
// already computes the document's canonical SHA-256 synchronously,
// streamed via io.TeeReader while the object is written to MinIO — never
// a second pass, never deferred to a background job — and the document
// row is only ever committed with that hash already known. Master
// prompt's "Document Hashing" section is explicit that this is the
// correct, required design ("do not create a background flow that
// allows a document to be considered committed before its canonical
// SHA-256 is known") — there is no safe asynchronous version of this
// step to build, so none was built.
//
// Redaction (internal/service.DocumentService.RedactDocument, System 8)
// decodes/redacts/re-encodes an image entirely in memory, bounded by the
// same DocumentsConfig.MaxUploadSize every upload already respects — a
// single-image, sub-second CPU operation for any file size this system
// accepts, not a genuinely long-running or resource-exhausting one.
// Moving it to a background job would also force POST /documents/:id/
// redact's current, simple "the derivative exists and is returned in
// this response" contract into an asynchronous poll/SSE pattern for no
// corresponding benefit — exactly the "do not move operations to
// asynchronous processing simply for the sake of having more jobs"
// master prompt warns against.
//
// This project also has no thumbnail-generation or OCR/metadata-
// extraction pipeline implemented by any system through 11 — inventing a
// DOCUMENT_THUMBNAIL_GENERATION or DOCUMENT_METADATA_EXTRACTION task type
// for a feature that does not otherwise exist would be scope creep, not
// infrastructure. If a future system adds one, and it turns out to be
// genuinely expensive, it belongs on this package's infrastructure,
// following AUDIT_CHAIN_VERIFY's existing pattern (TypeVerifyAuditChain/
// audit_verification.go) rather than inventing a second one. See
// docs/BACKGROUND_JOBS.md's "Task Types" for the full reasoning.
