// Package blockchain provides the Hyperledger Fabric blockchain integration
// for Evidentia (System 20). It exposes a clean Service interface that the
// rest of the application depends on, with two implementations:
//
//   - FabricService: the real Hyperledger Fabric Gateway implementation.
//     Active when FABRIC_ENABLED=true.
//
//   - NoopService: a safe, no-op fallback. Active when FABRIC_ENABLED=false.
//     All operations return a "blockchain unavailable" sentinel error so
//     callers can distinguish "blockchain down" from "document tampered" —
//     master prompt §15/§16's explicit requirement. No fake transaction IDs,
//     no silent success.
//
// The rest of the application imports this package and depends on Service,
// never on the Fabric SDK directly. This makes it testable with a mock and
// allows the entire blockchain layer to be disabled without touching any
// handler or service.
package blockchain

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrUnavailable is returned by every Service method when the Fabric network
// is not configured or not reachable. Callers must treat this as
// BLOCKCHAIN_UNAVAILABLE (the document itself is NOT tampered — the
// blockchain layer is simply not available to confirm or deny anything).
var ErrUnavailable = errors.New("blockchain: Hyperledger Fabric network is unavailable")

// ErrNotFound is returned when a GetProvenance/GetHistory call succeeds but
// the requested evidence ID has no record on the ledger.
var ErrNotFound = errors.New("blockchain: no evidence anchor found on ledger")

// ErrIntegrityMismatch is returned when a VerifyEvidence call finds that
// the hash stored on the ledger does not match the hash provided by the
// caller. This is BLOCKCHAIN_MISMATCH (distinct from HASH_MISMATCH, which
// is a PostgreSQL vs. file discrepancy).
var ErrIntegrityMismatch = errors.New("blockchain: hash on ledger does not match provided hash")

// AnchorRequest carries the minimum information needed to anchor a document
// event on the Fabric ledger. Never includes document content, PII, or
// credentials.
type AnchorRequest struct {
	// EvidenceID is the Evidentia document UUID. Used as the primary
	// key on the ledger. Must be deterministic (never randomized per-call)
	// so the chaincode can detect and reject duplicate logical anchors.
	EvidenceID uuid.UUID

	// CaseReference is a privacy-safe case identifier. For System 20 this
	// is the case UUID; a real deployment might hash or redact it.
	CaseReference string

	// Version is the document version number (1 for initial upload, 2 for
	// first redaction, etc.). Combined with EvidenceID, provides a stable
	// idempotency key.
	Version int32

	// Hash is the SHA-256 digest of the document, encoded as lowercase hex.
	// This is the value that ends up on the ledger — not the raw bytes.
	Hash string

	// EventType is the action that triggered this anchor, e.g.
	// "DOCUMENT_UPLOADED", "DOCUMENT_REDACTED", "CERTIFICATE_ISSUED".
	EventType string

	// ActorOrg is the MSP organization ID of the actor submitting this
	// anchor (e.g. "PoliceMSP"). Set by the FabricService from config.
	ActorOrg string

	// AppRecordID is the UUID of the anchor row in PostgreSQL
	// (blockchain_anchors.id), used to correlate back from the ledger.
	AppRecordID uuid.UUID

	// PreviousVersion is the version number of the preceding anchor for
	// this evidence, enabling explicit version chain verification. 0 means
	// this is the first anchor.
	PreviousVersion int32
}

// AnchorResult is the successful outcome of an AnchorEvidence call.
type AnchorResult struct {
	// TransactionID is the Fabric transaction ID returned by the peer.
	// This is the real ledger transaction ID — never fabricated.
	TransactionID string

	// Organization is the MSP org that submitted and endorsed the tx.
	Organization string

	// Timestamp is when the transaction was committed (block timestamp).
	Timestamp time.Time
}

// VerifyRequest carries the evidence reference to verify on the ledger.
type VerifyRequest struct {
	EvidenceID uuid.UUID
	Version    int32
	// Hash is the expected SHA-256 hex-encoded hash to compare against.
	Hash string
}

// VerifyResult is the outcome of a VerifyEvidence call.
type VerifyResult struct {
	// OnChainHash is the hash recorded on the ledger.
	OnChainHash string

	// Match is true if OnChainHash == the hash in VerifyRequest.
	Match bool

	// TransactionID of the original anchor transaction.
	TransactionID string

	// AnchoredAt is when the evidence was originally anchored.
	AnchoredAt time.Time

	// Organization that submitted the original anchor.
	Organization string
}

// ProvenanceEntry is one entry in the provenance history of a document.
type ProvenanceEntry struct {
	EvidenceID    string
	Version       int32
	EventType     string
	Hash          string
	Organization  string
	Timestamp     time.Time
	TransactionID string
	AppRecordID   string
}

// Service is the blockchain integration interface. Every method must
// return ErrUnavailable if the network is not configured or reachable.
// Callers must never treat ErrUnavailable as evidence of tampering.
type Service interface {
	// AnchorEvidence submits a new evidence anchor to the Fabric ledger.
	// Returns ErrUnavailable if the network is not reachable.
	// Returns an error if the chaincode rejects the transaction (e.g. a
	// genuine duplicate anchor with conflicting data).
	AnchorEvidence(ctx context.Context, req AnchorRequest) (AnchorResult, error)

	// VerifyEvidence retrieves the ledger record for the given evidence
	// version and compares its hash against req.Hash.
	// Returns ErrNotFound if no anchor exists on the ledger.
	// Returns ErrIntegrityMismatch if the hashes differ.
	// Returns ErrUnavailable if the network is not reachable.
	VerifyEvidence(ctx context.Context, req VerifyRequest) (VerifyResult, error)

	// GetProvenance retrieves the most recent ledger record for an evidence
	// document. Returns ErrNotFound if no record exists.
	GetProvenance(ctx context.Context, evidenceID uuid.UUID) (*ProvenanceEntry, error)

	// GetHistory retrieves the full provenance history of an evidence
	// document across all versions and event types.
	GetHistory(ctx context.Context, evidenceID uuid.UUID) ([]ProvenanceEntry, error)

	// IsEnabled reports whether the Fabric integration is configured and
	// active. Callers may use this to decide whether to surface blockchain
	// UI elements.
	IsEnabled() bool

	// Close releases any underlying connections gracefully.
	Close() error
}

// HashToHex converts a raw 32-byte SHA-256 digest (as stored in PostgreSQL's
// BYTEA column) to the lowercase hex string that the chaincode expects and
// the API returns.
func HashToHex(raw []byte) string {
	return hex.EncodeToString(raw)
}

// HexToHash converts a lowercase hex string back to a 32-byte slice.
func HexToHash(s string) ([]byte, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("blockchain: invalid hex hash: %w", err)
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("blockchain: hash must be 32 bytes, got %d", len(b))
	}
	return b, nil
}
