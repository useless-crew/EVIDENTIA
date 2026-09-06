package service

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"evidentia/backend/db/generated"
	"evidentia/backend/internal/audit"
	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/authz"
	"evidentia/backend/internal/blockchain"
	"evidentia/backend/internal/events"
	"evidentia/backend/internal/jobs"
	"evidentia/backend/internal/models"
	"evidentia/backend/internal/repository"
	"evidentia/backend/internal/storage"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/hash"
)

// blockchainWorkerIdentityUserID is the worker-side RLS sentinel for
// blockchain_anchors writes — analogous to workerIdentityUserID in
// audit_service.go. b020 suffix = "blockchain / System 20". Never written
// into any column, never references a real users row.
var blockchainWorkerIdentityUserID = uuid.MustParse("00000000-0000-0000-0000-00000000b020")

// blockchainWorkerIdentity is the AppIdentity every background worker
// transaction uses for blockchain_anchors UPDATE calls. The UPDATE policy
// checks current_app_role() = 'ADMIN' only.
var blockchainWorkerIdentity = repository.AppIdentity{
	UserID: blockchainWorkerIdentityUserID,
	Role:   models.RoleAdmin,
}

// BlockchainEventTypes is the closed vocabulary of event_type values written
// to blockchain_anchors, matching the database constraint.
const (
	BlockchainEventDocumentUploaded  = "DOCUMENT_UPLOADED"
	BlockchainEventDocumentRedacted  = "DOCUMENT_REDACTED"
	BlockchainEventCertificateIssued = "CERTIFICATE_ISSUED"
)

// BlockchainAnchorStatus mirrors blockchain_anchors.status values.
const (
	BlockchainStatusPending   = "PENDING"
	BlockchainStatusConfirmed = "CONFIRMED"
	BlockchainStatusFailed    = "FAILED"
)

// BlockchainVerifyStatus is the structured result of a blockchain
// verification, combining PostgreSQL, file hash, and blockchain checks.
// These values are distinct (master prompt §15/§16) — BLOCKCHAIN_UNAVAILABLE
// must never collapse to TAMPERED.
const (
	BlockchainVerifyStatusVerified      = "VERIFIED"
	BlockchainVerifyStatusHashMismatch  = "HASH_MISMATCH"
	BlockchainVerifyStatusChainMismatch = "BLOCKCHAIN_MISMATCH"
	BlockchainVerifyStatusUnavailable   = "BLOCKCHAIN_UNAVAILABLE"
	BlockchainVerifyStatusNotAnchored   = "BLOCKCHAIN_NOT_ANCHORED"

	IntegrityStatusIntegrityFailure     = "INTEGRITY_FAILURE"
	IntegrityStatusVersionMismatch      = "VERSION_MISMATCH"
)

// BlockchainAnchorSummary is the safe, API-friendly shape of a
// blockchain_anchors row. Never exposes key material, internal network
// topology, or sensitive evidence content.
type BlockchainAnchorSummary struct {
	ID              uuid.UUID  `json:"id"`
	DocumentID      uuid.UUID  `json:"document_id"`
	DocumentVersion int32      `json:"document_version"`
	EventType       string     `json:"event_type"`
	DocumentHash    string     `json:"document_hash"` // hex-encoded
	Status          string     `json:"status"`
	FabricTxID      *string    `json:"fabric_tx_id,omitempty"`
	FabricChannel   *string    `json:"fabric_channel,omitempty"`
	FabricChaincode *string    `json:"fabric_chaincode,omitempty"`
	Organization    *string    `json:"organization,omitempty"`
	LastError       *string    `json:"last_error,omitempty"`
	RetryCount      int32      `json:"retry_count"`
	CreatedAt       time.Time  `json:"created_at"`
	ConfirmedAt     *time.Time `json:"confirmed_at,omitempty"`
}

// BlockchainVerifyResult is the structured outcome of POST /documents/:id/blockchain/verify
// and POST /documents/:id/verify-candidate.
type BlockchainVerifyResult struct {
	// Status is one of the BlockchainVerifyStatus* or IntegrityStatus* constants:
	// VERIFIED, HASH_MISMATCH, BLOCKCHAIN_MISMATCH, BLOCKCHAIN_UNAVAILABLE,
	// BLOCKCHAIN_NOT_ANCHORED, VERSION_MISMATCH, or INTEGRITY_FAILURE.
	Status string `json:"status"`

	// DocumentID is the document being verified.
	DocumentID uuid.UUID `json:"document_id"`

	// Version is the document version number.
	Version int32 `json:"version"`

	// ExpectedHash is the canonical hash recorded in PostgreSQL (hex string).
	ExpectedHash string `json:"expected_hash"`

	// ActualHash is the computed SHA-256 hash of the verification candidate or stored file (hex string).
	ActualHash string `json:"actual_hash"`

	// BlockchainHash is the hash recorded on the Fabric ledger, or nil if unavailable / not anchored.
	BlockchainHash *string `json:"blockchain_hash,omitempty"`

	// DatabaseMatch is true if ActualHash == ExpectedHash.
	DatabaseMatch bool `json:"database_match"`

	// BlockchainMatch is true if BlockchainHash != nil and *BlockchainHash == ActualHash.
	BlockchainMatch bool `json:"blockchain_match"`

	// BlockchainStatus indicates Fabric ledger status: MATCH, MISMATCH, UNAVAILABLE, or NOT_ANCHORED.
	BlockchainStatus string `json:"blockchain_status,omitempty"`

	// FileHash is an alias to ActualHash for backward compatibility.
	FileHash string `json:"file_hash"`

	// StoredHash is an alias to ExpectedHash for backward compatibility.
	StoredHash string `json:"stored_hash"`

	// ChainHash is an alias to BlockchainHash for backward compatibility.
	ChainHash *string `json:"chain_hash,omitempty"`

	// TransactionID is the Fabric tx_id (if blockchain-confirmed).
	TransactionID *string `json:"transaction_id,omitempty"`

	// AnchoredAt is when the ledger anchor was committed.
	AnchoredAt *time.Time `json:"anchored_at,omitempty"`

	// Organization is the MSP org that submitted the anchor.
	Organization *string `json:"organization,omitempty"`

	// BlockchainEnabled reports whether the Fabric integration is active.
	BlockchainEnabled bool `json:"blockchain_enabled"`

	// VerifiedAt is the timestamp of this verification check.
	VerifiedAt time.Time `json:"verified_at"`

	// Source is either "candidate" (uploaded file) or "stored" (MinIO).
	Source string `json:"source,omitempty"`

	// Details is a clear human-readable explanation of the verification outcome.
	Details string `json:"details,omitempty"`
}

// BlockchainAnchorService orchestrates evidence blockchain anchoring for
// Evidentia (System 20). It creates blockchain_anchors records (the outbox),
// enqueues Asynq tasks for async Fabric submission, and provides provenance/
// verification APIs.
//
// Separation from DocumentService:
//
//	DocumentService owns the document lifecycle and calls
//	BlockchainAnchorService.CreateAnchor after a successful upload — this
//	service never touches MinIO or the documents table directly.
//
// Failure policy:
//
//	Blockchain failures NEVER fail the primary document operation. A
//	PENDING anchor record is always created, and failures are reported
//	asynchronously. The caller (DocumentService) treats a blockchain anchor
//	enqueue failure as a loggable warning, not a user-facing error.
type BlockchainAnchorService struct {
	pool       *pgxpool.Pool
	authz      *authz.Service
	recorder   audit.Recorder
	blockchain blockchain.Service
	jobClient  *jobs.Client
	publisher  events.Publisher
	logger     *slog.Logger
	storage    storage.Storage
	channel    string
	chaincode  string
	mspID      string
}

func NewBlockchainAnchorService(
	pool *pgxpool.Pool,
	authzSvc *authz.Service,
	recorder audit.Recorder,
	blockchainSvc blockchain.Service,
	jobClient *jobs.Client,
	publisher events.Publisher,
	logger *slog.Logger,
	objStorage storage.Storage,
	channel, chaincode, mspID string,
) *BlockchainAnchorService {
	return &BlockchainAnchorService{
		pool:       pool,
		authz:      authzSvc,
		recorder:   recorder,
		blockchain: blockchainSvc,
		jobClient:  jobClient,
		publisher:  publisher,
		logger:     logger,
		storage:    objStorage,
		channel:    channel,
		chaincode:  chaincode,
		mspID:      mspID,
	}
}


// BlockchainService returns the underlying blockchain.Service.
func (s *BlockchainAnchorService) BlockchainService() blockchain.Service {
	return s.blockchain
}

// Channel returns the configured Fabric channel.
func (s *BlockchainAnchorService) Channel() string {
	return s.channel
}

// Chaincode returns the configured Fabric chaincode name.
func (s *BlockchainAnchorService) Chaincode() string {
	return s.chaincode
}

// MSPID returns the configured local MSP ID.
func (s *BlockchainAnchorService) MSPID() string {
	return s.mspID
}


// CreateAnchorForDocument creates a PENDING blockchain_anchors record and
// enqueues an Asynq task to submit it to the Fabric ledger. Called by
// DocumentService after a successful document upload.
//
// This method uses the uploading user's identity for the INSERT (so the
// RLS INSERT policy, which checks current_app_user_id() IS NOT NULL, is
// satisfied). The subsequent UPDATE (by the worker) uses blockchainWorkerIdentity.
//
// If the Asynq enqueue fails, the PENDING row remains and can be recovered
// by a future reconciliation pass. The document upload is NOT rolled back.
func (s *BlockchainAnchorService) CreateAnchorForDocument(
	ctx context.Context,
	user auth.AuthenticatedUser,
	documentID uuid.UUID,
	caseID uuid.UUID,
	sha256Hash []byte,
	eventType string,
) {
	if !s.blockchain.IsEnabled() {
		return
	}

	channel := s.channel
	chaincode := s.chaincode
	org := s.mspID

	ident := repository.AppIdentity{UserID: user.ID, Role: effectiveCaseRole(user)}
	var anchor generated.BlockchainAnchor
	err := repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		var txErr error
		anchor, txErr = q.CreateBlockchainAnchor(ctx, generated.CreateBlockchainAnchorParams{
			DocumentID:      documentID,
			DocumentVersion: 1,
			EventType:       eventType,
			DocumentHash:    sha256Hash,
			FabricChannel:   &channel,
			FabricChaincode: &chaincode,
			Organization:    &org,
		})
		return txErr
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "blockchain: failed to create anchor record",
			slog.String("document_id", documentID.String()),
			slog.String("event_type", eventType),
			slog.String("error", err.Error()),
		)
		return
	}

	s.recorder.Record(ctx, audit.Event{
		Action:       "BLOCKCHAIN_ANCHOR_REQUESTED",
		ResourceType: "blockchain_anchor",
		ResourceID:   &anchor.ID,
		UserID:       &user.ID,
		Role:         effectiveCaseRole(user),
		CaseID:       &caseID,
		Metadata: map[string]any{
			"document_id":   documentID.String(),
			"anchor_id":     anchor.ID.String(),
			"event_type":    eventType,
			"document_hash": hex.EncodeToString(sha256Hash),
		},
	})

	if enqErr := s.jobClient.EnqueueBlockchainAnchor(ctx, anchor.ID); enqErr != nil {
		s.logger.ErrorContext(ctx, "blockchain: failed to enqueue anchor job",
			slog.String("anchor_id", anchor.ID.String()),
			slog.String("document_id", documentID.String()),
			slog.String("error", enqErr.Error()),
		)
	}
}

// RunAnchor is called by the Asynq worker to submit a PENDING anchor to the
// Fabric ledger. It loads the anchor from PostgreSQL, submits the transaction,
// and updates the status to CONFIRMED or FAILED.
//
// Returns nil for both CONFIRMED and FAILED terminal outcomes (Asynq
// interprets a nil return as "task complete", and retries only on
// non-nil errors — matching the audit_service pattern exactly).
// Returns an error only for transient operational failures (DB unavailable,
// network glitch) that Asynq should retry.
func (s *BlockchainAnchorService) RunAnchor(ctx context.Context, anchorID uuid.UUID) error {
	// Load the anchor record using the worker identity (ADMIN) so the
	// UPDATE policy is satisfied.
	var anchor generated.BlockchainAnchor
	err := repository.WithTx(ctx, s.pool, blockchainWorkerIdentity, func(ctx context.Context, q *generated.Queries) error {
		var txErr error
		anchor, txErr = q.GetBlockchainAnchorByID(ctx, anchorID)
		return txErr
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Anchor deleted between enqueue and pickup — treat as permanent,
			// don't retry.
			s.logger.WarnContext(ctx, "blockchain: anchor not found",
				slog.String("anchor_id", anchorID.String()),
			)
			return nil
		}
		return fmt.Errorf("blockchain: load anchor %s: %w", anchorID, err)
	}

	if anchor.Status != BlockchainStatusPending {
		// Already confirmed or failed — this is an Asynq redeliver/retry
		// that arrived after the job already completed. Safe to ignore.
		return nil
	}

	// Load the document to get the case ID for the anchor request.
	var caseID uuid.UUID
	err = repository.WithTx(ctx, s.pool, blockchainWorkerIdentity, func(ctx context.Context, q *generated.Queries) error {
		doc, txErr := q.GetDocumentByID(ctx, anchor.DocumentID)
		if txErr != nil {
			return txErr
		}
		caseID = doc.CaseID
		return nil
	})
	if err != nil {
		return fmt.Errorf("blockchain: load document for anchor %s: %w", anchorID, err)
	}

	hashHex := blockchain.HashToHex(anchor.DocumentHash)
	req := blockchain.AnchorRequest{
		EvidenceID:    anchor.DocumentID,
		CaseReference: caseID.String(),
		Version:       anchor.DocumentVersion,
		Hash:          hashHex,
		EventType:     anchor.EventType,
		ActorOrg:      s.mspID,
		AppRecordID:   anchor.ID,
	}

	s.logger.InfoContext(ctx, "blockchain: submitting anchor",
		slog.String("anchor_id", anchorID.String()),
		slog.String("document_id", anchor.DocumentID.String()),
		slog.String("event_type", anchor.EventType),
	)

	result, err := s.blockchain.AnchorEvidence(ctx, req)
	if err != nil {
		errMsg := err.Error()
		s.logger.WarnContext(ctx, "blockchain: anchor submission failed",
			slog.String("anchor_id", anchorID.String()),
			slog.String("error", errMsg),
		)

		if errors.Is(err, blockchain.ErrUnavailable) {
			// Transient: let Asynq retry.
			if incErr := s.incrementRetry(ctx, anchorID, errMsg); incErr != nil {
				s.logger.ErrorContext(ctx, "blockchain: failed to increment retry count",
					slog.String("anchor_id", anchorID.String()),
					slog.String("error", incErr.Error()),
				)
			}
			return fmt.Errorf("blockchain: anchor %s: %w", anchorID, err)
		}

		// Permanent failure (chaincode rejection, invalid input, etc.)
		// — don't retry. Record as FAILED and return nil.
		s.markFailed(ctx, anchorID, errMsg, anchor.RetryCount+1)
		return nil
	}

	// Confirm the anchor in PostgreSQL.
	s.markConfirmed(ctx, anchorID, result.TransactionID, result.Organization)
	s.logger.InfoContext(ctx, "blockchain: anchor confirmed",
		slog.String("anchor_id", anchorID.String()),
		slog.String("tx_id", result.TransactionID),
	)

	s.recorder.Record(ctx, audit.Event{
		Action:       "BLOCKCHAIN_ANCHOR_CONFIRMED",
		ResourceType: "blockchain_anchor",
		ResourceID:   &anchorID,
		UserID:       nil,
		Role:         models.RoleAdmin,
		CaseID:       &caseID,
		Metadata: map[string]any{
			"anchor_id":   anchorID.String(),
			"tx_id":       result.TransactionID,
			"document_id": anchor.DocumentID.String(),
		},
	})

	s.publisher.Publish(ctx,
		events.TypeBlockchainAnchorConfirmed,
		events.ResourceTypeCase,
		caseID.String(),
		events.BlockchainAnchorData{
			AnchorID:   anchorID.String(),
			DocumentID: anchor.DocumentID.String(),
			CaseID:     caseID.String(),
			Status:     BlockchainStatusConfirmed,
			TxID:       result.TransactionID,
		},
	)

	return nil
}

// MarkAnchorOperationallyFailed is called by the Asynq error handler after
// all retries are exhausted for a transient failure. Records the anchor
// as FAILED, matching NewAuditVerificationErrorHandler's own pattern.
func (s *BlockchainAnchorService) MarkAnchorOperationallyFailed(ctx context.Context, anchorID uuid.UUID, cause error) error {
	errMsg := ""
	if cause != nil {
		errMsg = cause.Error()
	}
	return s.markFailed(ctx, anchorID, errMsg, -1)
}

// GetAnchorStatus returns the most recent blockchain anchor summary for a
// document, authorized by the caller's document access.
func (s *BlockchainAnchorService) GetAnchorStatus(
	ctx context.Context,
	user auth.AuthenticatedUser,
	documentID uuid.UUID,
) (*BlockchainAnchorSummary, error) {
	decision, err := s.authz.CanAccessDocument(ctx, user, documentID, authz.ActionDocumentVerify)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !decision.Allowed {
		return nil, utils.ErrForbidden(genericDocumentForbiddenMessage)
	}

	ident := repository.AppIdentity{UserID: user.ID, Role: effectiveCaseRole(user)}
	var anchor generated.BlockchainAnchor
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		var txErr error
		anchor, txErr = q.GetLatestBlockchainAnchorByDocumentID(ctx, documentID)
		return txErr
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // no anchor yet — not an error
		}
		return nil, utils.ErrInternal(err)
	}

	summary := toBlockchainAnchorSummary(anchor)
	return &summary, nil
}

// GetAnchorProvenance returns the full anchor history for a document,
// combining PostgreSQL records with (if available) the Fabric ledger.
func (s *BlockchainAnchorService) GetAnchorProvenance(
	ctx context.Context,
	user auth.AuthenticatedUser,
	documentID uuid.UUID,
) ([]BlockchainAnchorSummary, error) {
	decision, err := s.authz.CanAccessDocument(ctx, user, documentID, authz.ActionDocumentVerify)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !decision.Allowed {
		return nil, utils.ErrForbidden(genericDocumentForbiddenMessage)
	}

	ident := repository.AppIdentity{UserID: user.ID, Role: effectiveCaseRole(user)}
	var anchors []generated.BlockchainAnchor
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		var txErr error
		anchors, txErr = q.ListBlockchainAnchorsByDocumentID(ctx, documentID)
		return txErr
	})
	if err != nil {
		return nil, utils.ErrInternal(err)
	}

	summaries := make([]BlockchainAnchorSummary, 0, len(anchors))
	for _, a := range anchors {
		summaries = append(summaries, toBlockchainAnchorSummary(a))
	}
	return summaries, nil
}

// VerifyDocumentBlockchain performs a three-way integrity check:
//  1. Recompute the document's SHA-256 from MinIO and compare to PostgreSQL.
//  2. If a blockchain anchor exists and Fabric is available, compare to ledger.
//
// Returns a BlockchainVerifyResult with a granular status that precisely
// identifies which layer disagrees (if any). Never collapses
// BLOCKCHAIN_UNAVAILABLE into TAMPERED.
func (s *BlockchainAnchorService) VerifyDocumentBlockchain(
	ctx context.Context,
	user auth.AuthenticatedUser,
	documentID uuid.UUID,
) (*BlockchainVerifyResult, error) {
	res, err := s.VerifyCandidate(ctx, user, documentID, nil, 0, "", true)
	if err != nil {
		return nil, err
	}
	// For pure blockchain verification route, if DB matches but Fabric is unavailable/not anchored,
	// reflect that in the top-level Status for backward compatibility with existing callers.
	if res.DatabaseMatch {
		if res.BlockchainStatus == BlockchainVerifyStatusUnavailable {
			res.Status = BlockchainVerifyStatusUnavailable
		} else if res.BlockchainStatus == BlockchainVerifyStatusNotAnchored {
			res.Status = BlockchainVerifyStatusNotAnchored
		}
	}
	return res, nil
}

// VerifyCandidate performs an integrity verification of a document against:
//  1. PostgreSQL evidence metadata (canonical documents.sha256_hash)
//  2. Hyperledger Fabric blockchain anchor, when Fabric is enabled
//  3. Evidence version
//
// If isStored is true, it recomputes the SHA-256 hash from the stored object in MinIO.
// If isStored is false, it streams the candidate file from candidateReader up to maxCandidateSize
// without saving or persisting it to storage, database, or disk (guaranteeing original evidence immutability).
// It records audit events (DOCUMENT_VERIFICATION_REQUESTED and DOCUMENT_INTEGRITY_FAILURE / DOCUMENT_VERIFIED)
// and returns a structured BlockchainVerifyResult without ever modifying or overwriting the original evidence.
func (s *BlockchainAnchorService) VerifyCandidate(
	ctx context.Context,
	user auth.AuthenticatedUser,
	documentID uuid.UUID,
	candidateReader io.Reader,
	maxCandidateSize int64,
	candidateFilename string,
	isStored bool,
) (*BlockchainVerifyResult, error) {
	decision, err := s.authz.CanAccessDocument(ctx, user, documentID, authz.ActionDocumentVerify)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !decision.Allowed {
		return nil, utils.ErrForbidden(genericDocumentForbiddenMessage)
	}

	role := effectiveCaseRole(user)
	ident := repository.AppIdentity{UserID: user.ID, Role: role}
	var doc generated.Document
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		var txErr error
		doc, txErr = q.GetDocumentByID(ctx, documentID)
		return txErr
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, utils.ErrForbidden(genericDocumentForbiddenMessage)
		}
		return nil, utils.ErrInternal(err)
	}

	sourceStr := "candidate"
	if isStored {
		sourceStr = "stored"
	}

	// Audit event: DOCUMENT_VERIFICATION_REQUESTED (server-controlled)
	s.recorder.Record(ctx, audit.Event{
		Action:       "DOCUMENT_VERIFICATION_REQUESTED",
		ResourceType: "document",
		ResourceID:   &documentID,
		UserID:       &user.ID,
		Role:         role,
		CaseID:       &doc.CaseID,
		Metadata: map[string]any{
			"source":   sourceStr,
			"filename": candidateFilename,
		},
	})

	storedHashHex := blockchain.HashToHex(doc.Sha256Hash)
	verifiedAt := time.Now().UTC()

	// Compute candidate or stored hash
	var computedHex string
	if isStored {
		recomputedHash, storageErr := recomputeDocumentHash(ctx, s.storage, doc)
		if storageErr != nil {
			s.logger.ErrorContext(ctx, "blockchain verify: recompute document hash failed",
				slog.String("document_id", documentID.String()),
				slog.String("error", storageErr.Error()),
			)
			return nil, utils.ErrServiceUnavailable("The document could not be retrieved for verification")
		}
		computedHex = hex.EncodeToString(recomputedHash)
	} else {
		if candidateReader == nil {
			return nil, utils.ErrBadRequest("Missing verification candidate stream")
		}
		if maxCandidateSize <= 0 {
			maxCandidateSize = 52428800 // 50 MiB default
		}
		lr := &io.LimitedReader{R: candidateReader, N: maxCandidateSize + 1}
		h := hash.New()
		n, copyErr := io.Copy(h, lr)
		if copyErr != nil {
			return nil, utils.ErrBadRequest("Failed to read candidate file stream")
		}
		if n > maxCandidateSize {
			return nil, utils.ErrRequestEntityTooLarge("Verification candidate exceeds maximum allowable size")
		}
		computedHex = hex.EncodeToString(h.Sum(nil))
	}

	databaseMatch := (computedHex == storedHashHex)

	// If verifying stored file and mismatch occurs, reconcile tamper status.
	// When verifying candidate file, NEVER mutate documents.status (original remains immutable).
	if isStored {
		_ = reconcileTamperStatus(ctx, s.pool, ident, doc, databaseMatch)
	}

	// Fetch anchor version if available
	var docVersion int32 = 1
	var latestAnchor *generated.BlockchainAnchor
	var anchorFound bool

	anchErr := repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		anc, txErr := q.GetLatestBlockchainAnchorByDocumentID(ctx, documentID)
		if txErr == nil {
			latestAnchor = &anc
			anchorFound = true
			docVersion = anc.DocumentVersion
		} else if errors.Is(txErr, pgx.ErrNoRows) {
			return nil
		}
		return txErr
	})
	if anchErr != nil && !errors.Is(anchErr, pgx.ErrNoRows) {
		s.logger.WarnContext(ctx, "verify candidate: query blockchain anchor warning", slog.String("error", anchErr.Error()))
	}

	var (
		chainHash        *string
		txID             *string
		anchoredAt       *time.Time
		org              *string
		blockchainStatus string = BlockchainVerifyStatusNotAnchored
		blockchainMatch  bool   = false
		finalStatus      string = BlockchainVerifyStatusVerified
		details          string = ""
	)

	if !s.blockchain.IsEnabled() {
		blockchainStatus = BlockchainVerifyStatusUnavailable
	} else if !anchorFound || latestAnchor == nil || latestAnchor.Status != BlockchainStatusConfirmed {
		blockchainStatus = BlockchainVerifyStatusNotAnchored
	} else {
		// Query Fabric ledger
		verifyReq := blockchain.VerifyRequest{
			EvidenceID: documentID,
			Version:    latestAnchor.DocumentVersion,
			Hash:       storedHashHex,
		}
		verifyResult, fErr := s.blockchain.VerifyEvidence(ctx, verifyReq)
		if fErr != nil {
			if errors.Is(fErr, blockchain.ErrUnavailable) {
				blockchainStatus = BlockchainVerifyStatusUnavailable
			} else if errors.Is(fErr, blockchain.ErrNotFound) {
				blockchainStatus = BlockchainVerifyStatusNotAnchored
			} else if errors.Is(fErr, blockchain.ErrIntegrityMismatch) {
				blockchainStatus = BlockchainVerifyStatusChainMismatch
				chainHash = &verifyResult.OnChainHash
				txID = &verifyResult.TransactionID
				anchoredAt = &verifyResult.AnchoredAt
				org = &verifyResult.Organization
			} else {
				s.logger.WarnContext(ctx, "verify candidate: fabric verify error", slog.String("error", fErr.Error()))
				blockchainStatus = BlockchainVerifyStatusUnavailable
			}
		} else {
			blockchainStatus = "MATCH"
			chainHash = &verifyResult.OnChainHash
			txID = &verifyResult.TransactionID
			anchoredAt = &verifyResult.AnchoredAt
			org = &verifyResult.Organization
		}
	}

	if chainHash != nil && *chainHash == computedHex {
		blockchainMatch = true
	}

	// Status resolution
	if !databaseMatch {
		if !isStored {
			finalStatus = IntegrityStatusIntegrityFailure
		} else {
			finalStatus = BlockchainVerifyStatusHashMismatch
		}
		if blockchainMatch {
			details = "Evidence modification detected in database record. Submitted file matches Hyperledger Fabric anchor."
		} else {
			details = "The submitted file differs from the cryptographic fingerprint recorded when the evidence was registered."
		}
	} else {
		if blockchainStatus == BlockchainVerifyStatusChainMismatch {
			finalStatus = BlockchainVerifyStatusChainMismatch
			details = "The ledger record differs from the database fingerprint."
		} else {
			finalStatus = BlockchainVerifyStatusVerified
			if blockchainStatus == BlockchainVerifyStatusUnavailable {
				details = "Evidence integrity confirmed against PostgreSQL database hash. Blockchain verification is currently unavailable."
			} else if blockchainStatus == BlockchainVerifyStatusNotAnchored {
				details = "Evidence integrity confirmed against PostgreSQL database hash. Document is not anchored on blockchain."
			} else {
				details = "Evidence integrity confirmed. All cryptographic proofs match (Database and Blockchain)."
			}
		}
	}

	// Record completion audit events
	if finalStatus == IntegrityStatusIntegrityFailure || finalStatus == BlockchainVerifyStatusHashMismatch {
		s.recorder.Record(ctx, audit.Event{
			Action:       "DOCUMENT_INTEGRITY_FAILURE",
			ResourceType: "document",
			ResourceID:   &documentID,
			UserID:       &user.ID,
			Role:         role,
			CaseID:       &doc.CaseID,
			Metadata: map[string]any{
				"expected_hash": storedHashHex,
				"actual_hash":   computedHex,
				"source":        sourceStr,
				"result":        finalStatus,
			},
		})
	} else if finalStatus == BlockchainVerifyStatusChainMismatch {
		s.recorder.Record(ctx, audit.Event{
			Action:       "BLOCKCHAIN_INTEGRITY_FAILURE",
			ResourceType: "document",
			ResourceID:   &documentID,
			UserID:       &user.ID,
			Role:         role,
			CaseID:       &doc.CaseID,
			Metadata: map[string]any{
				"expected_hash": storedHashHex,
				"chain_hash":    chainHash,
				"source":        sourceStr,
				"result":        BlockchainVerifyStatusChainMismatch,
			},
		})
	} else {
		s.recorder.Record(ctx, audit.Event{
			Action:       "BLOCKCHAIN_VERIFICATION_COMPLETED",
			ResourceType: "document",
			ResourceID:   &documentID,
			UserID:       &user.ID,
			Role:         role,
			CaseID:       &doc.CaseID,
			Metadata: map[string]any{
				"result":            finalStatus,
				"source":            sourceStr,
				"blockchain_status": blockchainStatus,
			},
		})
	}

	return &BlockchainVerifyResult{
		Status:            finalStatus,
		DocumentID:        documentID,
		Version:           docVersion,
		ExpectedHash:      storedHashHex,
		ActualHash:        computedHex,
		BlockchainHash:    chainHash,
		DatabaseMatch:     databaseMatch,
		BlockchainMatch:   blockchainMatch,
		BlockchainStatus:  blockchainStatus,
		FileHash:          computedHex,
		StoredHash:        storedHashHex,
		ChainHash:         chainHash,
		TransactionID:     txID,
		AnchoredAt:        anchoredAt,
		Organization:      org,
		BlockchainEnabled: s.blockchain.IsEnabled(),
		VerifiedAt:        verifiedAt,
		Source:            sourceStr,
		Details:           details,
	}, nil
}

// IsBlockchainEnabled returns whether the Fabric integration is active.
func (s *BlockchainAnchorService) IsBlockchainEnabled() bool {
	return s.blockchain.IsEnabled()
}

// --- internal helpers ---

func (s *BlockchainAnchorService) markConfirmed(ctx context.Context, anchorID uuid.UUID, txID, org string) {
	err := repository.WithTx(ctx, s.pool, blockchainWorkerIdentity, func(ctx context.Context, q *generated.Queries) error {
		_, txErr := q.ConfirmBlockchainAnchor(ctx, generated.ConfirmBlockchainAnchorParams{
			ID:           anchorID,
			FabricTxID:   txID,
			Organization: org,
		})
		return txErr
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "blockchain: failed to confirm anchor in DB",
			slog.String("anchor_id", anchorID.String()),
			slog.String("tx_id", txID),
			slog.String("error", err.Error()),
		)
	}
}

func (s *BlockchainAnchorService) markFailed(ctx context.Context, anchorID uuid.UUID, errMsg string, retryCount int32) error {
	var errMsgPtr *string
	if errMsg != "" {
		errMsgPtr = &errMsg
	}
	return repository.WithTx(ctx, s.pool, blockchainWorkerIdentity, func(ctx context.Context, q *generated.Queries) error {
		_, txErr := q.FailBlockchainAnchor(ctx, generated.FailBlockchainAnchorParams{
			ID:         anchorID,
			LastError:  errMsgPtr,
			RetryCount: retryCount,
		})
		return txErr
	})
}

func (s *BlockchainAnchorService) incrementRetry(ctx context.Context, anchorID uuid.UUID, errMsg string) error {
	var errMsgPtr *string
	if errMsg != "" {
		errMsgPtr = &errMsg
	}
	return repository.WithTx(ctx, s.pool, blockchainWorkerIdentity, func(ctx context.Context, q *generated.Queries) error {
		return q.IncrementBlockchainAnchorRetry(ctx, generated.IncrementBlockchainAnchorRetryParams{
			ID:        anchorID,
			LastError: errMsgPtr,
		})
	})
}

func toBlockchainAnchorSummary(a generated.BlockchainAnchor) BlockchainAnchorSummary {
	s := BlockchainAnchorSummary{
		ID:              a.ID,
		DocumentID:      a.DocumentID,
		DocumentVersion: a.DocumentVersion,
		EventType:       a.EventType,
		DocumentHash:    blockchain.HashToHex(a.DocumentHash),
		Status:          a.Status,
		FabricTxID:      a.FabricTxID,
		FabricChannel:   a.FabricChannel,
		FabricChaincode: a.FabricChaincode,
		Organization:    a.Organization,
		LastError:       a.LastError,
		RetryCount:      a.RetryCount,
		CreatedAt:       a.CreatedAt,
	}
	if a.ConfirmedAt.Valid {
		t := a.ConfirmedAt.Time
		s.ConfirmedAt = &t
	}
	return s
}
