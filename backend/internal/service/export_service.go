package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"evidentia/backend/db/generated"
	"evidentia/backend/internal/audit"
	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/authz"
	"evidentia/backend/internal/repository"
	"evidentia/backend/internal/storage"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/hash"
)

// ExportedDocument is the success result for DownloadEvidenceExport
type ExportedDocument struct {
	Export   generated.EvidenceExport
	Document generated.Document
	Content  io.ReadCloser
}

type EvidenceExportSummary struct {
	ID                 uuid.UUID `json:"id"`
	ExportID           string    `json:"export_id"`
	DocumentID         uuid.UUID `json:"document_id"`
	Status             string    `json:"status"`
	ExportType         string    `json:"export_type"`
	WatermarkStatus    string    `json:"watermark_status"`
	CreatedAt          time.Time `json:"created_at"`
	ExportFingerprint  string    `json:"export_fingerprint"`
}

type ExportService struct {
	pool          *pgxpool.Pool
	authz         *authz.Service
	recorder      audit.Recorder
	storage       storage.Storage
	watermark     *WatermarkService
	logger        *slog.Logger
	blockchainSvc blockchainAnchorCreator
}

func NewExportService(pool *pgxpool.Pool, authzService *authz.Service, recorder audit.Recorder, objectStorage storage.Storage, watermark *WatermarkService, logger *slog.Logger) *ExportService {
	return &ExportService{
		pool:      pool,
		authz:     authzService,
		recorder:  recorder,
		storage:   objectStorage,
		watermark: watermark,
		logger:    logger,
	}
}

func (s *ExportService) SetBlockchainService(svc blockchainAnchorCreator) {
	s.blockchainSvc = svc
}

// RequestExport provisions an evidence export record and immediately processes it
// (streaming the document, watermarking it, and hashing the resulting bytes).
// It returns the Export ID, which the user can then download.
func (s *ExportService) RequestExport(ctx context.Context, user auth.AuthenticatedUser, documentID uuid.UUID, clientIP, userAgent string) (*EvidenceExportSummary, error) {
	// 1. Authorize user for document:download (using existing CanAccessDocument)
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
			return nil, utils.ErrForbidden(genericDocumentForbiddenMessage)
		}
		return nil, utils.ErrInternal(err)
	}

	// 2. Generate export identity and fingerprint
	exportUUID := uuid.New()
	exportID := fmt.Sprintf("EXP-%s-%d", exportUUID.String()[:8], time.Now().Unix())
	
	fingerprintBytes := make([]byte, 32)
	if _, err := rand.Read(fingerprintBytes); err != nil {
		return nil, utils.ErrInternal(fmt.Errorf("generate fingerprint: %w", err))
	}

	// 3. Retrieve original document from storage
	content, err := s.storage.Get(ctx, doc.StorageObjectKey)
	if err != nil {
		s.logger.ErrorContext(ctx, "document object missing for export", slog.String("document_id", doc.ID.String()))
		return nil, utils.ErrServiceUnavailable("The requested document is temporarily unavailable")
	}

	// 4. Apply Watermark via pipeline — structured payload so every field
	// is individually recoverable by the admin verification endpoint.
	wmPayload := WatermarkPayload{
		Version:     1,
		ExportID:    exportID,
		DocumentID:  doc.ID.String(),
		UserID:      user.ID.String(),
		ExportedAt:  time.Now().UTC().Format(time.RFC3339),
		ClientIP:    clientIP,
		Fingerprint: hex.EncodeToString(fingerprintBytes),
	}
	wmResult, err := s.watermark.ApplyWatermark(ctx, content, doc.MimeType, wmPayload)
	if err != nil {
		content.Close()
		return nil, utils.ErrInternal(fmt.Errorf("apply watermark: %w", err))
	}

	// 5. Stream to secondary storage bucket while computing hash of the export payload
	exportObjectKey := fmt.Sprintf("exports/%s/%s", doc.CaseID, exportID)
	
	hasher := hash.New()
	teed := io.TeeReader(wmResult.Stream, hasher)

	if putErr := s.storage.Put(ctx, exportObjectKey, teed, -1, doc.MimeType); putErr != nil {
		wmResult.Stream.Close()
		return nil, utils.ErrInternal(fmt.Errorf("store export: %w", putErr))
	}
	wmResult.Stream.Close()

	exportHash := hasher.Sum(nil)

	var created generated.EvidenceExport
	
	// 6. Persist Export Record
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		var sip, sua *string
		if clientIP != "" { sip = &clientIP }
		if userAgent != "" { sua = &userAgent }

		ex, err := q.CreateEvidenceExport(ctx, generated.CreateEvidenceExportParams{
			ExportID:             exportID,
			DocumentID:           doc.ID,
			DocumentVersion:      1,
			CaseID:               doc.CaseID,
			UserID:               user.ID,
			RecipientUserID:      user.ID, // Self-export for now. Share-exports would set this to the share recipient.
			ShareID:              nil,     
			OriginalSha256:       doc.Sha256Hash,
			ExportSha256:         exportHash,
			ExportFingerprint:    fingerprintBytes,
			WatermarkStatus:      wmResult.WatermarkStatus,
			WatermarkVersion:     wmResult.WatermarkVersion,
			ExportType:           "STANDARD",
			Permission:           "DOWNLOAD",
			SourceIP:             sip,
			UserAgent:            sua,
			SessionReferenceHash: nil,
			Status:               "COMPLETED",
			Metadata:             []byte(`{}`),
		})
		if err != nil {
			return err
		}
		
		// Update completion timestamp in DB
		now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
		ex, err = q.UpdateEvidenceExportStatus(ctx, generated.UpdateEvidenceExportStatusParams{
			ID: ex.ID, Status: "COMPLETED", ExportSha256: exportHash, CompletedAt: now,
		})
		created = ex
		return err
	})

	if err != nil {
		// Attempt orphan cleanup
		s.storage.Delete(ctx, exportObjectKey)
		return nil, utils.ErrInternal(fmt.Errorf("persist export metadata: %w", err))
	}

	// 7. Record Audit Event
	auditEvent := audit.Event{
		Action:       "EVIDENCE_EXPORTED",
		ResourceType: "evidence_export",
		ResourceID:   &created.ID,
		UserID:       &user.ID,
		Role:         effectiveCaseRole(user),
		CaseID:       &doc.CaseID,
		Metadata: map[string]any{
			"export_id":          exportID,
			"document_id":        doc.ID,
			"export_fingerprint": hex.EncodeToString(fingerprintBytes),
			"export_sha256":      hex.EncodeToString(exportHash),
			"watermark_status":   wmResult.WatermarkStatus,
		},
	}
	s.recorder.Record(ctx, auditEvent)

	// 8. System 20: Trigger Blockchain Anchor
	if s.blockchainSvc != nil {
		s.blockchainSvc.CreateAnchorForDocument(
			ctx,
			user,
			doc.ID,
			doc.CaseID,
			exportHash,
			"EVIDENCE_EXPORTED",
		)
	}

	return &EvidenceExportSummary{
		ID:                 created.ID,
		ExportID:           created.ExportID,
		DocumentID:         created.DocumentID,
		Status:             created.Status,
		ExportType:         created.ExportType,
		WatermarkStatus:    created.WatermarkStatus,
		CreatedAt:          created.CreatedAt,
		ExportFingerprint:  hex.EncodeToString(created.ExportFingerprint),
	}, nil
}

// DownloadEvidenceExport retrieves a previously generated export.
func (s *ExportService) DownloadEvidenceExport(ctx context.Context, user auth.AuthenticatedUser, exportID string) (*ExportedDocument, error) {
	ident := repository.AppIdentity{UserID: user.ID, Role: effectiveCaseRole(user)}
	var export generated.EvidenceExport
	var doc generated.Document

	err := repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		ex, err := q.GetEvidenceExportByExportID(ctx, exportID)
		if err != nil {
			return err
		}
		export = ex

		repo := repository.NewDocumentRepo(q)
		d, err := repo.GetByID(ctx, ex.DocumentID)
		doc = d
		return err
	})
	
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, utils.ErrForbidden("Export not found or access denied")
		}
		return nil, utils.ErrInternal(err)
	}

	if export.Status != "COMPLETED" {
		return nil, utils.ErrBadRequest("Export is not ready for download")
	}

	exportObjectKey := fmt.Sprintf("exports/%s/%s", doc.CaseID, export.ExportID)
	content, err := s.storage.Get(ctx, exportObjectKey)
	if err != nil {
		s.logger.ErrorContext(ctx, "export object missing", slog.String("export_id", exportID))
		return nil, utils.ErrServiceUnavailable("The requested export is temporarily unavailable")
	}

	// Record download of the export
	s.recorder.Record(ctx, audit.Event{
		Action:       "EXPORT_DOWNLOADED",
		ResourceType: "evidence_export",
		ResourceID:   &export.ID,
		UserID:       &user.ID,
		Role:         effectiveCaseRole(user),
		CaseID:       &doc.CaseID,
		Metadata: map[string]any{
			"export_id": exportID,
		},
	})

	return &ExportedDocument{
		Export:   export,
		Document: doc,
		Content:  content,
	}, nil
}

// WatermarkVerifyResult is returned by VerifyWatermark on success.
type WatermarkVerifyResult struct {
	Found       bool             `json:"found"`
	Payload     *WatermarkPayload `json:"payload,omitempty"`
	Message     string           `json:"message"`
}

// VerifyWatermark reads fileData, extracts the embedded AES-256-GCM
// watermark block, decrypts it, and returns the structured forensic metadata.
// It is admin-only: any non-admin caller receives a 403 from the router
// before this method is reached; this call records the verification attempt
// in the audit log regardless of the extraction outcome.
func (s *ExportService) VerifyWatermark(ctx context.Context, user auth.AuthenticatedUser, fileData []byte) (*WatermarkVerifyResult, error) {
	payload, err := s.watermark.ExtractWatermark(fileData)

	// Audit every verification attempt — found or not — so admins can see
	// who is checking files for watermarks and when.
	s.recorder.Record(ctx, audit.Event{
		Action:       "WATERMARK_VERIFIED",
		ResourceType: "evidence_export",
		UserID:       &user.ID,
		Role:         effectiveCaseRole(user),
		Metadata: map[string]any{
			"found":    err == nil,
			"file_size": len(fileData),
		},
	})

	if err != nil {
		return &WatermarkVerifyResult{
			Found:   false,
			Message: err.Error(),
		}, nil
	}

	return &WatermarkVerifyResult{
		Found:   true,
		Payload: payload,
		Message: "Forensic watermark verified successfully.",
	}, nil
}
