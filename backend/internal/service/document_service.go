package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"evidentia/backend/db/generated"
	"evidentia/backend/internal/audit"
	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/authz"
	"evidentia/backend/internal/events"
	"evidentia/backend/internal/models"
	"evidentia/backend/internal/repository"
	"evidentia/backend/internal/storage"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/hash"
)

// genericDocumentForbiddenMessage matches middleware's genericForbiddenMessage
// verbatim — see genericCaseForbiddenMessage's doc comment in
// case_service.go for why DocumentService re-checks authorization itself
// and must produce the identical client-facing response the middleware
// already would.
const genericDocumentForbiddenMessage = "You do not have permission to perform this action"

// documentTypes is the exact set of values documents_document_type_check
// allows (see db/migrations/000001_init_schema.up.sql) — mirrored here so
// validation can never drift from what the database itself accepts, the
// same pattern case_service.go's caseStatuses uses.
var documentTypes = map[string]bool{
	models.DocumentTypeFIR:              true,
	models.DocumentTypeForensicReport:   true,
	models.DocumentTypePhotoEvidence:    true,
	models.DocumentTypeWitnessStatement: true,
	models.DocumentTypeOther:            true,
}

const (
	maxDocumentDescriptionLen = 10_000
	maxDocumentFilenameLen    = 255
	// sniffLen bounds how many leading bytes of the upload are inspected
	// for MIME-type detection (http.DetectContentType only ever looks at
	// up to 512 bytes; peeking more would be wasted).
	sniffLen = 512
)

// deniedUploadMimeTypes rejects the handful of http.DetectContentType
// results that can carry active/executable content with no legitimate
// evidence use case — see docs/SECURITY.md's "Malicious documents". This
// is deliberately a DENYlist, not an ALLOWlist: an evidence platform must
// accept arbitrary forensic file formats (disk images, proprietary report
// formats, ...) that content sniffing cannot even recognize (falling back
// to "application/octet-stream"), and DownloadDocument already pairs
// every response with Content-Disposition: attachment plus
// X-Content-Type-Options: nosniff, so nothing accepted here is ever
// rendered/executed by a browser regardless of type. This denylist closes
// the remaining, narrower gap of storing an HTML document capable of
// carrying a <script> tag at all — rejected outright rather than merely
// relying on it never being served inline.
var deniedUploadMimeTypes = map[string]bool{
	"text/html; charset=utf-8": true,
	"text/html":                true,
}

// errUploadTooLarge is returned by the streaming size guard the moment a
// read would push the running total past the configured limit — see
// limitedReader below. It is checked with errors.Is from UploadDocument to
// distinguish "file too large" from any other storage failure.
var errUploadTooLarge = errors.New("document: upload exceeds maximum size")

// UploadDocumentInput is UploadDocument's request shape. File is the raw,
// not-yet-read multipart stream for the uploaded file's content — the
// caller (the HTTP handler) must not have consumed any of it. Filename is
// the client-supplied original filename, sanitized here, never trusted as
// a storage identity (master prompt §6/§31). There is deliberately no
// UploaderID/CaseID-as-owner field: the caller identity comes only from
// the authenticated user parameter to UploadDocument, never from this
// struct (master prompt §9: user_id/uploader_id are never client-supplied
// authoritative values).
type UploadDocumentInput struct {
	DocumentType string
	Description  *string
	Filename     string
	File         io.Reader
}

// DownloadedDocument is DownloadDocument's success result: metadata plus
// an open, not-yet-read object stream the caller (the HTTP handler) must
// Close and is responsible for streaming to the HTTP response.
type DownloadedDocument struct {
	Document generated.Document
	Content  io.ReadCloser
}

// DocumentService owns document business logic: multipart validation,
// server-side MIME detection, streaming SHA-256 computation, object
// storage, PostgreSQL metadata persistence, and audit integration. Like
// CaseService, it independently re-checks authorization via authz.Service
// rather than trusting that a caller already passed through
// middleware.RequireCaseAccess/RequireDocumentAccess — see
// docs/SECURITY.md's "Service-layer authorization is not optional here".
//
// System 6/7 boundary: this type computes and persists the INITIAL
// SHA-256 hash at ingestion (System 6), serves authorized downloads
// (System 6), and recomputes/compares that hash on demand to detect
// tampering (System 7's VerifyDocument). It does not implement
// redaction/derivative generation (a future redaction system) or the
// audit hash chain (System 8, per the numbering already established
// throughout Systems 2-5's code and the applied migration itself), and it
// never generates compliance certificates — that is
// CertificateService's job (internal/service/certificate_service.go),
// which depends on this type only via the shared, package-level
// recomputeDocumentHash function, not on DocumentService itself. See
// UploadDocument/DownloadDocument/VerifyDocument's doc comments for the
// exact boundary each respects.
type DocumentService struct {
	pool          *pgxpool.Pool
	authz         *authz.Service
	recorder      audit.Recorder
	storage       storage.Storage
	bucket        string
	maxUploadSize int64
	publisher     events.Publisher
	logger        *slog.Logger
}

func NewDocumentService(pool *pgxpool.Pool, authzService *authz.Service, recorder audit.Recorder, objectStorage storage.Storage, bucket string, maxUploadSize int64, publisher events.Publisher, logger *slog.Logger) *DocumentService {
	return &DocumentService{
		pool:          pool,
		authz:         authzService,
		recorder:      recorder,
		storage:       objectStorage,
		bucket:        bucket,
		maxUploadSize: maxUploadSize,
		publisher:     publisher,
		logger:        logger,
	}
}

// UploadDocument authorizes user for document:upload on caseID (RBAC
// permission AND case relationship — see authz.Service.CanAccessCase,
// reused here with authz.ActionDocumentUpload exactly as
// middleware.RequireCaseAccess already does for this route; master prompt
// §10's "ACTION AND CASE ACCESS" requirement is this single call), then:
// validates document_type/description, sanitizes the filename, generates
// the document's UUID, streams the file to object storage while computing
// its SHA-256 hash in the same pass (never buffering the whole file, never
// hashing anything but the raw bytes), and persists metadata in one
// transaction. A storage write that succeeds followed by a failed
// PostgreSQL insert triggers best-effort orphan-object cleanup (master
// prompt §16/§45) — the object is deleted; if that also fails, the
// orphaned-object condition is logged operationally (never silently
// dropped), and the client still sees a failure either way, never a false
// "uploaded successfully" response.
//
// This method computes the INITIAL hash only — comparing it against a
// freshly recomputed one to detect tampering is System 7's job.
func (s *DocumentService) UploadDocument(ctx context.Context, user auth.AuthenticatedUser, caseID uuid.UUID, input UploadDocumentInput) (*DocumentSummary, error) {
	decision, err := s.authz.CanAccessCase(ctx, user, caseID, authz.ActionDocumentUpload)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !decision.Allowed {
		return nil, utils.ErrForbidden(genericDocumentForbiddenMessage)
	}

	if !documentTypes[input.DocumentType] {
		return nil, utils.ErrBadRequest("Invalid document_type")
	}
	if input.Description != nil {
		if len(*input.Description) > maxDocumentDescriptionLen {
			return nil, utils.ErrBadRequest(fmt.Sprintf("description must be at most %d characters", maxDocumentDescriptionLen))
		}
		if !utf8.ValidString(*input.Description) {
			return nil, utils.ErrBadRequest("description must be valid UTF-8")
		}
	}
	if input.File == nil {
		return nil, utils.ErrBadRequest("file is required")
	}

	filename := sanitizeFilename(input.Filename)
	documentID := uuid.New()
	objectKey := documentObjectKey(caseID, documentID)

	size, sha256Sum, detectedMime, err := s.streamToStorage(ctx, objectKey, input.File)
	if err != nil {
		// Best-effort cleanup regardless of failure cause: some Storage
		// implementations (e.g. local disk) may have written partial bytes
		// before the error occurred. Storage.Delete on a key that was
		// never created is a documented no-op, so this is always safe to
		// attempt (master prompt §17: never leave an orphan unhandled).
		s.cleanupOrphan(ctx, objectKey, caseID, documentID, "upload stream failed before completion")

		if errors.Is(err, errUploadTooLarge) {
			return nil, utils.NewAppError(413, utils.CodeRequestEntityTooLarge,
				fmt.Sprintf("File exceeds the maximum upload size of %d bytes", s.maxUploadSize), nil)
		}
		return nil, utils.ErrInternal(fmt.Errorf("store document: %w", err))
	}

	if deniedUploadMimeTypes[detectedMime] {
		s.cleanupOrphan(ctx, objectKey, caseID, documentID, "detected content type is not permitted")
		return nil, utils.ErrUnprocessableEntity(fmt.Sprintf("Files detected as %q are not accepted", detectedMime))
	}

	ident := repository.AppIdentity{UserID: user.ID, Role: effectiveCaseRole(user)}
	var created generated.Document
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		repo := repository.NewDocumentRepo(q)
		d, err := repo.Create(ctx, generated.CreateDocumentParams{
			ID:               documentID,
			CaseID:           caseID,
			ParentDocumentID: nil, // original upload — redaction derivatives are a future system's job
			DocumentType:     input.DocumentType,
			Filename:         filename,
			Description:      input.Description,
			MimeType:         detectedMime,
			FileSize:         size,
			Sha256Hash:       sha256Sum,
			StorageBucket:    s.bucket,
			StorageObjectKey: objectKey,
			Metadata:         []byte(`{}`),
			UploadedBy:       user.ID, // server-controlled — never req/client-supplied
		})
		created = d
		return err
	})
	if err != nil {
		s.cleanupOrphan(ctx, objectKey, caseID, documentID, "postgresql insert failed after object storage succeeded")
		return nil, utils.ErrInternal(fmt.Errorf("persist document metadata: %w", err))
	}

	role := effectiveCaseRole(user)
	s.recorder.Record(ctx, audit.Event{
		Action:       "DOCUMENT_UPLOADED",
		ResourceType: "document",
		ResourceID:   &created.ID,
		UserID:       &user.ID,
		Role:         role,
		CaseID:       &caseID,
		Metadata: map[string]any{
			"filename":      created.Filename,
			"document_type": created.DocumentType,
			"file_size":     created.FileSize,
			"mime_type":     created.MimeType,
			"sha256_hash":   hex.EncodeToString(created.Sha256Hash),
		},
	})

	summary := toDocumentSummary(created)
	return &summary, nil
}

// DownloadDocument authorizes user for document:download on documentID
// (RBAC permission AND the document's case relationship — see
// authz.Service.CanAccessDocument), then loads the document's metadata
// under the caller's own RLS identity and retrieves its object from
// storage — in that order (master prompt §54: never touch object storage
// before the database authorization decision succeeds). The audit event
// is recorded once the object stream is confirmed available, not after
// the caller finishes reading it (master prompt §36: never require the
// full file to be loaded to record the action). The caller owns the
// returned Content and must Close it.
//
// A document row whose object is missing from storage (master prompt
// §45's "DB row exists but MinIO object missing") is logged operationally
// and reported to the client as a generic, non-leaky service-unavailable
// error — never a raw storage driver error, never silence.
func (s *DocumentService) DownloadDocument(ctx context.Context, user auth.AuthenticatedUser, documentID uuid.UUID) (*DownloadedDocument, error) {
	decision, err := s.authz.CanAccessDocument(ctx, user, documentID, authz.ActionDocumentDownload)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !decision.Allowed {
		return nil, utils.ErrForbidden(genericDocumentForbiddenMessage)
	}

	ident := repository.AppIdentity{UserID: user.ID, Role: effectiveCaseRole(user)}
	var doc generated.Document
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		repo := repository.NewDocumentRepo(q)
		d, err := repo.GetByID(ctx, documentID)
		doc = d
		return err
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Already authorized by CanAccessDocument above — a row that
			// vanishes between that check and this read is the same
			// anti-enumeration posture as "not found", never a 404.
			return nil, utils.ErrForbidden(genericDocumentForbiddenMessage)
		}
		return nil, utils.ErrInternal(err)
	}

	content, err := s.storage.Get(ctx, doc.StorageObjectKey)
	if err != nil {
		s.logger.ErrorContext(ctx, "document object missing or unreachable in storage",
			slog.String("document_id", doc.ID.String()),
			slog.String("case_id", doc.CaseID.String()),
			slog.String("storage_object_key", doc.StorageObjectKey),
			slog.String("error", err.Error()),
		)
		return nil, utils.ErrServiceUnavailable("The requested document is temporarily unavailable")
	}

	s.recorder.Record(ctx, audit.Event{
		Action:       "DOCUMENT_DOWNLOADED",
		ResourceType: "document",
		ResourceID:   &doc.ID,
		UserID:       &user.ID,
		Role:         effectiveCaseRole(user),
		CaseID:       &doc.CaseID,
		Metadata:     map[string]any{"filename": doc.Filename},
	})

	return &DownloadedDocument{Document: doc, Content: content}, nil
}

// Verification status values — System 7's entire vocabulary for a
// verification result. Deliberately not a database column/enum: a
// verification is a computed-on-request comparison, not persisted state
// (the persisted side effect, when it occurs, is documents.status moving
// to/from models.DocumentStatusTampered — see VerifyDocument).
const (
	VerificationStatusVerified         = "VERIFIED"
	VerificationStatusIntegrityFailure = "INTEGRITY_FAILURE"
)

// VerificationResult is POST /documents/:id/verify's response shape.
// Both StoredHash and ComputedHash are always populated (hex-encoded) —
// including on failure — so a client/investigator can see exactly what
// diverged, without exposing anything beyond the two digests themselves
// (no storage_bucket/storage_object_key, no internal error detail).
type VerificationResult struct {
	DocumentID   uuid.UUID `json:"document_id"`
	Status       string    `json:"status"`
	StoredHash   string    `json:"stored_hash"`
	ComputedHash string    `json:"computed_hash"`
	VerifiedAt   time.Time `json:"verified_at"`
}

// VerifyDocument authorizes user for document:verify on documentID (RBAC
// permission AND the document's case relationship — see
// authz.Service.CanAccessDocument), loads the document's canonical hash
// under RLS, retrieves the actual stored object, and recomputes its
// SHA-256 — the ONLY question this method answers is "does the evidence
// PostgreSQL and MinIO currently agree it holds still match", per master
// prompt §3.
//
// This is the critical distinction master prompt §5 draws: a STORAGE
// ERROR (the object could not be retrieved/hashed at all — MinIO down, a
// missing object) is returned as an *error* (utils.ErrServiceUnavailable/
// ErrInternal), never as a verification status. An INTEGRITY FAILURE (the
// object WAS retrieved and hashed successfully, but the digest differs
// from documents.sha256_hash) is a successful, meaningful verification
// outcome — returned as a normal (nil-error) VerificationResult with
// Status == VerificationStatusIntegrityFailure, exactly like a VERIFIED
// result structurally, differing only in which status string it carries.
// A caller must not confuse "the request failed" with "the request
// succeeded and reports tampering".
//
// The canonical hash (documents.sha256_hash) is NEVER written here,
// regardless of outcome — see master prompt §3.6/§12: a mismatch is
// never "repaired". The only column this method may update is
// documents.status, moving it to models.DocumentStatusTampered on a
// mismatch (a value db/migrations/000001_init_schema.up.sql's own
// comment already reserves for exactly this) or back to
// models.DocumentStatusActive if a previously-tampered document now
// verifies again (the state should always reflect the CURRENT truth, not
// a permanent, un-refreshable flag) — and only when the current value
// actually needs to change, so a repeated, identical-outcome verification
// does not churn the row.
func (s *DocumentService) VerifyDocument(ctx context.Context, user auth.AuthenticatedUser, documentID uuid.UUID) (*VerificationResult, error) {
	decision, err := s.authz.CanAccessDocument(ctx, user, documentID, authz.ActionDocumentVerify)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !decision.Allowed {
		return nil, utils.ErrForbidden(genericDocumentForbiddenMessage)
	}

	ident := repository.AppIdentity{UserID: user.ID, Role: effectiveCaseRole(user)}
	var doc generated.Document
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		repo := repository.NewDocumentRepo(q)
		d, err := repo.GetByID(ctx, documentID)
		doc = d
		return err
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, utils.ErrForbidden(genericDocumentForbiddenMessage)
		}
		return nil, utils.ErrInternal(err)
	}

	computedHash, err := recomputeDocumentHash(ctx, s.storage, doc)
	if err != nil {
		s.logger.ErrorContext(ctx, "document verification: could not recompute hash from stored object",
			slog.String("document_id", doc.ID.String()),
			slog.String("case_id", doc.CaseID.String()),
			slog.String("storage_object_key", doc.StorageObjectKey),
			slog.String("error", err.Error()),
		)
		// Storage error, NOT an integrity finding — master prompt §5.
		return nil, utils.ErrServiceUnavailable("The document could not be retrieved for verification")
	}

	verifiedAt := time.Now().UTC()
	role := effectiveCaseRole(user)
	matches := bytes.Equal(computedHash, doc.Sha256Hash)

	if err := reconcileTamperStatus(ctx, s.pool, ident, doc, matches); err != nil {
		return nil, utils.ErrInternal(err)
	}

	result := &VerificationResult{
		DocumentID:   doc.ID,
		StoredHash:   hex.EncodeToString(doc.Sha256Hash),
		ComputedHash: hex.EncodeToString(computedHash),
		VerifiedAt:   verifiedAt,
	}

	if !matches {
		result.Status = VerificationStatusIntegrityFailure
		s.recorder.Record(ctx, audit.Event{
			Action:       "DOCUMENT_INTEGRITY_FAILURE",
			ResourceType: "document",
			ResourceID:   &doc.ID,
			UserID:       &user.ID,
			Role:         role,
			CaseID:       &doc.CaseID,
			Metadata: map[string]any{
				"stored_hash":   result.StoredHash,
				"computed_hash": result.ComputedHash,
			},
		})
		s.publisher.Publish(ctx, events.TypeDocumentVerificationCompleted, events.ResourceTypeCase, doc.CaseID.String(), events.DocumentVerificationData{
			DocumentID: doc.ID.String(), CaseID: doc.CaseID.String(), Result: events.DocumentVerificationResultIntegrityFailure,
		})
		return result, nil
	}

	result.Status = VerificationStatusVerified
	s.recorder.Record(ctx, audit.Event{
		Action:       "DOCUMENT_VERIFIED",
		ResourceType: "document",
		ResourceID:   &doc.ID,
		UserID:       &user.ID,
		Role:         role,
		CaseID:       &doc.CaseID,
		Metadata:     map[string]any{"sha256_hash": result.StoredHash},
	})
	s.publisher.Publish(ctx, events.TypeDocumentVerificationCompleted, events.ResourceTypeCase, doc.CaseID.String(), events.DocumentVerificationData{
		DocumentID: doc.ID.String(), CaseID: doc.CaseID.String(), Result: events.DocumentVerificationResultVerified,
	})
	return result, nil
}

// reconcileTamperStatus updates documents.status to reflect the CURRENT
// verification truth — TAMPERED on a mismatch, ACTIVE on a match — but
// only issues an UPDATE when the stored status doesn't already reflect
// that outcome, so repeated identical-result verifications don't churn
// the row (updated_at, etc.) for no reason. Never touches sha256_hash,
// storage_bucket, storage_object_key, or any other column. Package-level
// (not a DocumentService method) so CertificateService's own hash check
// can call it too, without depending on DocumentService — both entry
// points must react to a discovered mismatch identically.
func reconcileTamperStatus(ctx context.Context, pool *pgxpool.Pool, ident repository.AppIdentity, doc generated.Document, matches bool) error {
	var wantStatus string
	switch {
	case !matches && doc.Status != models.DocumentStatusTampered:
		wantStatus = models.DocumentStatusTampered
	case matches && doc.Status == models.DocumentStatusTampered:
		wantStatus = models.DocumentStatusActive
	default:
		return nil
	}

	return repository.WithTx(ctx, pool, ident, func(ctx context.Context, q *generated.Queries) error {
		return repository.NewDocumentRepo(q).UpdateStatus(ctx, doc.ID, wantStatus)
	})
}

// recomputeDocumentHash retrieves doc's stored object from objStorage and
// streams it through SHA-256 — never io.ReadAll, never loading the whole
// object into memory at once (master prompt §5/§32). This is the single
// shared core both VerifyDocument and CertificateService's generation
// path use, so the two entry points' independent authorization checks
// (document:verify vs certificate:create) never have to duplicate, or risk
// diverging on, this streaming/hashing logic. It is a package-level
// function, not a DocumentService method, specifically so CertificateService
// can call it without depending on DocumentService at all.
func recomputeDocumentHash(ctx context.Context, objStorage storage.Storage, doc generated.Document) ([]byte, error) {
	reader, err := objStorage.Get(ctx, doc.StorageObjectKey)
	if err != nil {
		return nil, fmt.Errorf("retrieve stored object: %w", err)
	}
	defer reader.Close()

	hasher := hash.New()
	if _, err := io.Copy(hasher, reader); err != nil {
		return nil, fmt.Errorf("read stored object: %w", err)
	}
	return hasher.Sum(nil), nil
}

// ---- internal helpers ----

// streamToStorage reads r to EOF exactly once, computing its SHA-256
// digest and writing it to object storage in the same pass (via
// io.TeeReader — never buffering the whole file, never reading it twice).
// A read that would push the running byte count past s.maxUploadSize
// aborts the stream with errUploadTooLarge before any further bytes are
// requested from r.
func (s *DocumentService) streamToStorage(ctx context.Context, objectKey string, r io.Reader) (size int64, sha256Sum []byte, detectedMime string, err error) {
	sniffed, rest, err := sniffContentType(r)
	if err != nil {
		return 0, nil, "", fmt.Errorf("read upload stream: %w", err)
	}

	limited := &limitedReader{r: rest, limit: s.maxUploadSize}
	hasher := hash.New()
	teed := io.TeeReader(limited, hasher)

	if putErr := s.storage.Put(ctx, objectKey, teed, -1, sniffed); putErr != nil {
		if limited.exceeded {
			return 0, nil, "", errUploadTooLarge
		}
		return 0, nil, "", putErr
	}

	return limited.n, hasher.Sum(nil), sniffed, nil
}

// cleanupOrphan best-effort deletes an object that was successfully
// written to storage but whose corresponding PostgreSQL row failed to
// persist — master prompt §16/§45's orphan-cleanup requirement. A cleanup
// failure is logged operationally at ERROR level with enough identifying
// detail (case/document ID, object key) for manual reconciliation; it is
// never escalated into a different client-facing error than the original
// persistence failure, and it never causes UploadDocument to falsely
// report success.
func (s *DocumentService) cleanupOrphan(ctx context.Context, objectKey string, caseID, documentID uuid.UUID, reason string) {
	if delErr := s.storage.Delete(ctx, objectKey); delErr != nil {
		s.logger.ErrorContext(ctx, "orphaned document object could not be cleaned up — manual reconciliation required",
			slog.String("case_id", caseID.String()),
			slog.String("document_id", documentID.String()),
			slog.String("storage_object_key", objectKey),
			slog.String("reason", reason),
			slog.String("cleanup_error", delErr.Error()),
		)
		return
	}
	s.logger.WarnContext(ctx, "cleaned up orphaned document object after metadata persistence failure",
		slog.String("case_id", caseID.String()),
		slog.String("document_id", documentID.String()),
		slog.String("storage_object_key", objectKey),
		slog.String("reason", reason),
	)
}

// documentObjectKey builds the deterministic, server-generated storage key
// for an original (never-redacted) document upload — see master prompt
// §6/§7. caseID/documentID are always server-resolved UUIDs, never
// client-supplied path fragments, so there is nothing here for a client
// to manipulate into path traversal.
func documentObjectKey(caseID, documentID uuid.UUID) string {
	return fmt.Sprintf("cases/%s/documents/%s/original", caseID, documentID)
}

// sanitizeFilename reduces a client-supplied filename to a safe display
// value: strips any directory component (path traversal — master prompt
// §31), strips control characters (including CR/LF, closing off header-
// injection via a later Content-Disposition), collapses to a bounded
// length, and falls back to a generic name if nothing safe remains. The
// result is metadata only — it is never used as the storage key (see
// documentObjectKey), so no sanitization here can turn a hostile filename
// into an object-storage or filesystem hazard.
func sanitizeFilename(name string) string {
	// Strip any directory component under EITHER separator convention,
	// regardless of the host OS: filepath.Base alone only recognizes the
	// CURRENT OS's separator, so a Windows-style "..\x" path would pass
	// through unchanged when this server runs on Linux (confirmed by
	// TestSanitizeFilename_StripsPathTraversal). Normalizing "\" to "/"
	// first, then taking everything after the last "/", handles both.
	name = strings.ReplaceAll(strings.TrimSpace(name), `\`, "/")
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}

	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			continue // control chars, including CR/LF — never in a stored filename
		}
		b.WriteRune(r)
	}
	cleaned := strings.TrimSpace(b.String())

	if cleaned == "" || isAllDots(cleaned) {
		return "document"
	}
	if len(cleaned) > maxDocumentFilenameLen {
		cleaned = cleaned[:maxDocumentFilenameLen]
	}
	return cleaned
}

// isAllDots reports whether s consists only of '.' characters (".", "..",
// "...", ...) — filepath-meaningful "current/parent directory" tokens
// that must never survive as a stored filename, even once no path
// separator remains to make that obvious.
func isAllDots(s string) bool {
	for _, r := range s {
		if r != '.' {
			return false
		}
	}
	return true
}

// sniffContentType peeks up to sniffLen bytes from r to detect its MIME
// type via http.DetectContentType (content-based, never trusting a
// client-declared Content-Type header — master prompt §32), then returns
// a reader that still yields those peeked bytes followed by the rest of
// r — no bytes are lost or consumed twice.
func sniffContentType(r io.Reader) (contentType string, rest io.Reader, err error) {
	buf := make([]byte, sniffLen)
	n, readErr := io.ReadFull(r, buf)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
		return "", nil, readErr
	}
	buf = buf[:n]
	return http.DetectContentType(buf), io.MultiReader(bytes.NewReader(buf), r), nil
}

// limitedReader wraps r, erroring with errUploadTooLarge the moment more
// than limit bytes have been read — the streaming equivalent of rejecting
// an oversized file before allocating unbounded memory/storage for it
// (master prompt §12).
type limitedReader struct {
	r        io.Reader
	limit    int64
	n        int64
	exceeded bool
}

// Read deliberately does NOT pre-clamp p to the remaining budget: doing so
// makes it impossible to distinguish "exactly limit bytes, then a natural
// EOF" from "more than limit bytes exist" (a file of exactly limit bytes
// would otherwise still request a final, always-empty read that this type
// would wrongly report as an overage). Instead, each Read is allowed to
// proceed and is checked AFTER the fact: if the running total exceeds
// limit, the call is failed. The worst-case overshoot is bounded by one
// caller-sized buffer (typically io.Copy's ~32KB, or io.ReadAll's current
// growth step) — negligible next to the multi-MB/GB limits this guards
// against, and still nowhere close to "unbounded".
func (l *limitedReader) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	l.n += int64(n)
	if l.n > l.limit {
		l.exceeded = true
		return n, errUploadTooLarge
	}
	return n, err
}

func toDocumentSummary(d generated.Document) DocumentSummary {
	return DocumentSummary{
		ID:               d.ID,
		CaseID:           d.CaseID,
		DocumentType:     d.DocumentType,
		Filename:         d.Filename,
		Description:      d.Description,
		MimeType:         d.MimeType,
		FileSize:         d.FileSize,
		Sha256Hash:       hex.EncodeToString(d.Sha256Hash),
		Status:           d.Status,
		ParentDocumentID: d.ParentDocumentID,
		UploadedBy:       d.UploadedBy,
		UploadedAt:       d.UploadedAt,
	}
}
