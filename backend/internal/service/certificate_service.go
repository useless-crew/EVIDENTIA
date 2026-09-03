package service

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"evidentia/backend/db/generated"
	"evidentia/backend/internal/audit"
	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/authz"
	"evidentia/backend/internal/repository"
	"evidentia/backend/internal/storage"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/crypto"
)

// certificateFormatVersion identifies the certificate PAYLOAD/schema
// format this service produces — NOT a document version, which the
// existing schema has no concept of (System 2 tracks case/document
// lineage only via documents.parent_document_id, for a future redaction
// system). A certificate's binding to a specific state of the evidence is
// expressed entirely through DocumentHash, which is what
// compliance_certificates_document_hash_unique
// (000003_certificate_integrity.up.sql) actually enforces uniqueness on.
const certificateFormatVersion = "1.0"

const signatureAlgorithmECDSA = "ECDSA-P256-SHA256"

// certificateIssuer identifies the issuing system in every certificate's
// signed payload — this platform, not the individual user who triggered
// generation (that identity is already captured separately, and
// immutably, via compliance_certificates.generated_by).
const certificateIssuer = "Evidentia"

// certificatePayloadData is the shape persisted in
// compliance_certificates.certificate_data. It holds everything about a
// certificate that ISN'T already its own database column (id, document_id,
// document_hash, certificate_version, generated_by, generated_at) —
// avoiding storing the same fact twice in two different representations.
type certificatePayloadData struct {
	SignatureAlgorithm string `json:"signature_algorithm"`
	Signature          string `json:"signature,omitempty"` // hex-encoded ASN.1 DER
	Issuer             string `json:"issuer"`
}

// CertificateSummary is the compliance-certificate API response shape —
// never the raw generated.ComplianceCertificate (which would require the
// caller to know to unmarshal certificate_data itself). The signing
// PRIVATE key is never reachable from this type or anywhere else this
// service exposes.
type CertificateSummary struct {
	ID                 uuid.UUID `json:"id"`
	DocumentID         uuid.UUID `json:"document_id"`
	DocumentHash       string    `json:"document_hash"`
	CertificateVersion string    `json:"certificate_version"`
	SignatureAlgorithm string    `json:"signature_algorithm"`
	Signature          string    `json:"signature,omitempty"`
	Issuer             string    `json:"issuer"`
	GeneratedBy        uuid.UUID `json:"generated_by"`
	GeneratedAt        time.Time `json:"generated_at"`
}

// CertificateVerificationResult is VerifyCertificateIntegrity's result —
// see that method's doc comment. Exported for test/internal-caller
// coverage per master prompt §19 ("provide the verification capability
// internally... without inventing a public API unless clearly
// justified") — no HTTP route exposes this directly today.
type CertificateVerificationResult struct {
	// HashMatches reports whether the certificate's bound document_hash
	// equals the hash passed in (typically the document's CURRENT
	// canonical hash) — the structural binding check from master prompt
	// §19 ("certificate.document_hash == document.sha256_hash").
	HashMatches bool
	// SignatureChecked is false only if the certificate was persisted
	// with no signature at all (SignatureAlgorithm == "" — not possible
	// via GetOrCreateCertificate today, since a key, ephemeral or
	// configured, is always available, but kept honest for any row
	// created before signing existed or by a future, deliberately
	// unsigned path).
	SignatureChecked bool
	// SignatureValid is meaningless (always false) when SignatureChecked
	// is false — check SignatureChecked first.
	SignatureValid bool
}

// CertificateService owns compliance-certificate business logic:
// authorization, re-verifying the bound document's integrity before
// generating a certificate, cryptographically signing the certificate
// payload, persisting it, and audit integration. It depends on
// DocumentService only through the shared, package-level
// recomputeDocumentHash/reconcileTamperStatus functions — never on
// DocumentService itself — so a discovered mismatch is handled identically
// regardless of whether it was found via POST /documents/:id/verify or
// while generating a certificate, without the two services needing a
// direct dependency on each other.
//
// System 7 boundary: this type binds a certificate to the EXACT document
// hash verified at generation time and never issues one for a document
// that fails that check. It does not implement §65B-specific legal
// certificate formatting, PDF rendering, or any output format beyond the
// JSON API response — those, if ever needed, are a later system's concern
// building on the hash-binding and signature this type already provides.
type CertificateService struct {
	pool       *pgxpool.Pool
	authz      *authz.Service
	recorder   audit.Recorder
	storage    storage.Storage
	signingKey *ecdsa.PrivateKey
	logger     *slog.Logger
}

// NewCertificateService constructs a CertificateService. If signingKeyPEM
// is empty, a fresh ECDSA key is generated for this process's lifetime
// only (logged once, at WARN, so an operator notices — see
// config.CertificateConfig's doc comment for why this is a deliberate,
// non-fatal fallback rather than refusing to start) — certificates
// generated in this mode carry a real, internally self-consistent
// signature, but one that cannot be verified against a stable public key
// across a process restart. If signingKeyPEM is non-empty but fails to
// parse as a PEM-encoded PKCS#8 ECDSA private key, construction fails:
// per master prompt §9, a misconfigured secret must never be silently
// downgraded to an insecure fallback.
func NewCertificateService(pool *pgxpool.Pool, authzService *authz.Service, recorder audit.Recorder, objectStorage storage.Storage, signingKeyPEM string, logger *slog.Logger) (*CertificateService, error) {
	var key *ecdsa.PrivateKey
	if signingKeyPEM == "" {
		k, err := crypto.GenerateECDSAKey()
		if err != nil {
			return nil, fmt.Errorf("certificate service: generate ephemeral signing key: %w", err)
		}
		key = k
		logger.Warn("CERTIFICATE_SIGNING_KEY not configured — using an ephemeral, process-lifetime-only ECDSA key; certificates signed now cannot be verified after a restart. Set CERTIFICATE_SIGNING_KEY for a persistent key.")
	} else {
		k, err := crypto.ParseECDSAPrivateKeyPEM([]byte(signingKeyPEM))
		if err != nil {
			return nil, fmt.Errorf("certificate service: parse configured signing key: %w", err)
		}
		key = k
	}

	return &CertificateService{
		pool:       pool,
		authz:      authzService,
		recorder:   recorder,
		storage:    objectStorage,
		signingKey: key,
		logger:     logger,
	}, nil
}

// GetOrCreateCertificate authorizes user for certificate:read on
// documentID (RBAC permission AND the document's case relationship — see
// authz.Service.CanAccessDocument), then:
//
//  1. Returns the existing certificate bound to the document's CURRENT
//     canonical hash, if one exists.
//  2. Otherwise, if user ALSO holds certificate:create for this document
//     (a second, independent CanAccessDocument check — RBAC-wise,
//     certificate:create is a materially different, more privileged
//     action than certificate:read, per the seed data only ADMIN holds
//     both), attempts to generate one: recomputes the document's hash
//     from its stored object, refuses (utils.ErrConflict, never a "valid"
//     certificate) if it no longer matches the canonical hash, and
//     otherwise creates a certificate cryptographically bound to that
//     exact hash.
//  3. Otherwise (no existing certificate, and user lacks certificate:create)
//     returns utils.ErrNotFound — indistinguishable from "not generated
//     yet" to a reader who was never going to be allowed to generate one
//     anyway, so this never leaks the create/read permission split to the
//     client.
//
// This is the ONLY endpoint/method that creates a certificate — matching
// the existing handler stub's own framing ("retrieval/generation
// trigger") and master prompt §16's instruction not to invent a
// separate, unnecessary create endpoint.
func (s *CertificateService) GetOrCreateCertificate(ctx context.Context, user auth.AuthenticatedUser, documentID uuid.UUID) (*CertificateSummary, error) {
	readDecision, err := s.authz.CanAccessDocument(ctx, user, documentID, authz.ActionCertificateRead)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !readDecision.Allowed {
		return nil, utils.ErrForbidden(genericDocumentForbiddenMessage)
	}

	ident := repository.AppIdentity{UserID: user.ID, Role: effectiveCaseRole(user)}
	var doc generated.Document
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		d, err := repository.NewDocumentRepo(q).GetByID(ctx, documentID)
		doc = d
		return err
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, utils.ErrForbidden(genericDocumentForbiddenMessage)
		}
		return nil, utils.ErrInternal(err)
	}

	existing, found, err := s.fetchCertificate(ctx, ident, doc.ID, doc.Sha256Hash)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if found {
		summary := toCertificateSummary(existing)
		return &summary, nil
	}

	createDecision, err := s.authz.CanAccessDocument(ctx, user, documentID, authz.ActionCertificateCreate)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !createDecision.Allowed {
		return nil, utils.ErrNotFound("No compliance certificate exists for this document")
	}

	return s.generateCertificate(ctx, ident, user, doc)
}

// generateCertificate re-verifies doc's integrity (never trusting that the
// hash checked at some earlier point is still valid — master prompt §15:
// "Certificate generation should ... Compare against canonical database
// hash. Refuse certificate generation if mismatch") and, only on a match,
// creates a certificate signed over a canonical payload.
func (s *CertificateService) generateCertificate(ctx context.Context, ident repository.AppIdentity, user auth.AuthenticatedUser, doc generated.Document) (*CertificateSummary, error) {
	computedHash, err := recomputeDocumentHash(ctx, s.storage, doc)
	if err != nil {
		s.logger.ErrorContext(ctx, "certificate generation: could not recompute document hash from stored object",
			slog.String("document_id", doc.ID.String()),
			slog.String("storage_object_key", doc.StorageObjectKey),
			slog.String("error", err.Error()),
		)
		return nil, utils.ErrServiceUnavailable("The document could not be retrieved to generate a certificate")
	}

	matches := bytes.Equal(computedHash, doc.Sha256Hash)
	if err := reconcileTamperStatus(ctx, s.pool, ident, doc, matches); err != nil {
		return nil, utils.ErrInternal(err)
	}

	role := effectiveCaseRole(user)
	if !matches {
		s.recorder.Record(ctx, audit.Event{
			Action:       "DOCUMENT_INTEGRITY_FAILURE",
			ResourceType: "document",
			ResourceID:   &doc.ID,
			UserID:       &user.ID,
			Role:         role,
			CaseID:       &doc.CaseID,
			Metadata: map[string]any{
				"stored_hash":   hex.EncodeToString(doc.Sha256Hash),
				"computed_hash": hex.EncodeToString(computedHash),
				"context":       "certificate_generation",
			},
		})
		return nil, utils.ErrConflict("Cannot generate a compliance certificate: the document failed integrity verification")
	}

	certID := uuid.New()
	// Truncated to microseconds — PostgreSQL's timestamptz resolution
	// (000003_certificate_integrity.up.sql's generated_at column) — so the
	// value signed here and the value later read back from
	// compliance_certificates.generated_at are bit-for-bit identical. Without
	// this, Go's nanosecond-precision time.Now() would sign a payload whose
	// issued_at field differs from the one VerifyCertificateIntegrity later
	// reconstructs from the persisted (microsecond-truncated) row, making
	// every certificate's signature spuriously fail re-verification after a
	// database round-trip.
	issuedAt := time.Now().UTC().Truncate(time.Microsecond)
	payload := canonicalCertificatePayload(certID, doc.ID, doc.Sha256Hash, certificateFormatVersion, issuedAt, certificateIssuer, user.ID)
	sig, err := crypto.SignECDSA(s.signingKey, payload)
	if err != nil {
		return nil, utils.ErrInternal(fmt.Errorf("sign certificate payload: %w", err))
	}

	dataJSON, err := json.Marshal(certificatePayloadData{
		SignatureAlgorithm: signatureAlgorithmECDSA,
		Signature:          hex.EncodeToString(sig),
		Issuer:             certificateIssuer,
	})
	if err != nil {
		return nil, utils.ErrInternal(fmt.Errorf("marshal certificate data: %w", err))
	}

	var created generated.ComplianceCertificate
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		repo := repository.NewCertificateRepo(q)
		c, err := repo.Create(ctx, generated.CreateCertificateParams{
			ID:                 certID,
			DocumentID:         doc.ID,
			DocumentHash:       doc.Sha256Hash,
			CertificateVersion: certificateFormatVersion,
			CertificateData:    dataJSON,
			GeneratedBy:        user.ID,
			GeneratedAt:        issuedAt,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// ON CONFLICT DO NOTHING matched: a concurrent request won
				// the race for this exact (document_id, document_hash) pair
				// (master prompt §23). Not an error — fetch and return the
				// winning row so this call is idempotent.
				existing, err := repo.GetByDocumentAndHash(ctx, doc.ID, doc.Sha256Hash)
				if err != nil {
					return err
				}
				created = existing
				return nil
			}
			return err
		}
		created = c
		return nil
	})
	if err != nil {
		return nil, utils.ErrInternal(fmt.Errorf("persist compliance certificate: %w", err))
	}

	s.recorder.Record(ctx, audit.Event{
		Action:       "CERTIFICATE_CREATED",
		ResourceType: "compliance_certificate",
		ResourceID:   &created.ID,
		UserID:       &user.ID,
		Role:         role,
		CaseID:       &doc.CaseID,
		Metadata: map[string]any{
			"document_id":   doc.ID.String(),
			"document_hash": hex.EncodeToString(doc.Sha256Hash),
		},
	})

	summary := toCertificateSummary(created)
	return &summary, nil
}

// fetchCertificate looks up the certificate bound to (documentID,
// documentHash) under the caller's own RLS identity, distinguishing "does
// not exist" (found=false, err=nil) from a genuine database error.
func (s *CertificateService) fetchCertificate(ctx context.Context, ident repository.AppIdentity, documentID uuid.UUID, documentHash []byte) (generated.ComplianceCertificate, bool, error) {
	var cert generated.ComplianceCertificate
	found := false
	err := repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		c, err := repository.NewCertificateRepo(q).GetByDocumentAndHash(ctx, documentID, documentHash)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		cert = c
		found = true
		return nil
	})
	return cert, found, err
}

// VerifyCertificateIntegrity determines whether cert genuinely corresponds
// to currentDocumentHash — master prompt §19: "A certificate must never
// be treated as valid merely because its database record exists." No
// public HTTP route calls this today (see this file's package doc and
// master prompt §19's fallback: "provide the verification capability
// internally... without inventing a public API"); it exists for direct
// test coverage and for a future caller (e.g. a future certificate
// verification endpoint, or System 9's audit-chain tooling) to reuse
// without reimplementing this logic.
func (s *CertificateService) VerifyCertificateIntegrity(cert generated.ComplianceCertificate, currentDocumentHash []byte) (CertificateVerificationResult, error) {
	result := CertificateVerificationResult{
		HashMatches: bytes.Equal(cert.DocumentHash, currentDocumentHash),
	}

	var data certificatePayloadData
	if err := json.Unmarshal(cert.CertificateData, &data); err != nil {
		return result, fmt.Errorf("parse certificate_data: %w", err)
	}
	if data.SignatureAlgorithm == "" || data.Signature == "" {
		return result, nil
	}

	sigBytes, err := hex.DecodeString(data.Signature)
	if err != nil {
		return result, fmt.Errorf("decode certificate signature: %w", err)
	}

	result.SignatureChecked = true
	payload := canonicalCertificatePayload(cert.ID, cert.DocumentID, cert.DocumentHash, cert.CertificateVersion, cert.GeneratedAt, data.Issuer, cert.GeneratedBy)
	result.SignatureValid = crypto.VerifyECDSA(&s.signingKey.PublicKey, payload, sigBytes)
	return result, nil
}

// canonicalCertificatePayload builds the exact, deterministic byte
// sequence that is signed (at generation) and reconstructed (at
// verification) for a certificate — fixed field order and format, never
// arbitrary JSON marshaling (whose field/key order is not a stable
// contract in Go or any language — master prompt §18). Every input here
// is either an existing, immutable database column or a value fixed at
// generation time and persisted verbatim (issuedAt, issuer) — nothing
// about this payload can drift between signing and later verification.
func canonicalCertificatePayload(certID, documentID uuid.UUID, documentHash []byte, certVersion string, issuedAt time.Time, issuer string, generatedBy uuid.UUID) []byte {
	return []byte(fmt.Sprintf(
		"evidentia-compliance-certificate\ncertificate_id=%s\ndocument_id=%s\ndocument_hash=%s\ncertificate_version=%s\nissued_at=%s\nissuer=%s\ngenerated_by=%s",
		certID, documentID, hex.EncodeToString(documentHash), certVersion, issuedAt.UTC().Format(time.RFC3339Nano), issuer, generatedBy,
	))
}

func toCertificateSummary(c generated.ComplianceCertificate) CertificateSummary {
	var data certificatePayloadData
	_ = json.Unmarshal(c.CertificateData, &data) // best-effort; zero-value fields on failure, never an error surfaced to the caller for a read

	return CertificateSummary{
		ID:                 c.ID,
		DocumentID:         c.DocumentID,
		DocumentHash:       hex.EncodeToString(c.DocumentHash),
		CertificateVersion: c.CertificateVersion,
		SignatureAlgorithm: data.SignatureAlgorithm,
		Signature:          data.Signature,
		Issuer:             data.Issuer,
		GeneratedBy:        c.GeneratedBy,
		GeneratedAt:        c.GeneratedAt,
	}
}
