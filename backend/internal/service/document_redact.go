// Document redaction: producing an independent, redacted derivative of an
// existing evidence document without ever modifying the source.
//
// Supported formats are deliberately narrow: image/png and image/jpeg
// only, where "redact" means the requested rectangular regions are
// genuinely overwritten (opaque black, draw.Src — a destructive replace,
// never an alpha-blended overlay) in the derivative's actual re-encoded
// pixel data before it is hashed and stored. Every other stored mime_type
// — including application/pdf — is refused with utils.ErrUnprocessableEntity.
// This project has no verified, safe way to strip underlying text/vector
// content from a PDF (or any other format) today; presenting a black box
// drawn merely on TOP of unmodified content would be a fake redaction that
// still leaks the "removed" content to anyone who extracts the underlying
// bytes, which is strictly worse than refusing the request outright.
package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"evidentia/backend/db/generated"
	"evidentia/backend/internal/audit"
	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/authz"
	"evidentia/backend/internal/repository"
	"evidentia/backend/internal/storage"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/hash"
)

const (
	// maxRedactionRegions bounds how many rectangles a single redaction
	// request may carry — a generous limit for real redaction workloads
	// while still refusing a pathological/abusive payload (master prompt
	// §7/§29).
	maxRedactionRegions = 50
	// minRedactionReasonLen/maxRedactionReasonLen bound the required
	// redaction reason — a redaction is a deliberate, accountable act
	// (master prompt §20), never an empty or unbounded string.
	minRedactionReasonLen = 3
	maxRedactionReasonLen = 2000
)

// supportedRedactionFormats maps a document's stored mime_type (recorded
// at upload time via content-sniffing — see DocumentService.streamToStorage)
// to the Go standard-library image format name image.Decode reports for
// it. Deliberately not a superset of every format the document pipeline
// accepts generally (see models.DocumentType's much broader real-world
// document types) — only formats this file can genuinely redact belong
// here. Extending this map requires actually implementing (and testing)
// real content removal for the new format, never just adding an entry.
var supportedRedactionFormats = map[string]string{
	"image/png":  "png",
	"image/jpeg": "jpeg",
}

// RedactRegion is one rectangular region to redact, expressed in the
// SOURCE image's own pixel coordinate space — never a rendered/zoomed
// screen coordinate; a caller (e.g. the frontend) must convert from
// on-screen coordinates to real image pixels before sending this. Page
// exists for forward compatibility with a future multi-page redaction
// format but must currently be exactly 1 — every presently-supported
// format (supportedRedactionFormats) is single-page raster. JSON tags here
// ARE the wire contract for both the HTTP request body and the persisted
// redactions.region_data column (see RedactDocument) — there is
// deliberately only one shape, never a separate API type re-mapped to
// this one.
type RedactRegion struct {
	Page   int     `json:"page"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// RedactDocumentInput is RedactDocument's request shape.
type RedactDocumentInput struct {
	Reason  string
	Regions []RedactRegion
}

// RedactionSummary is POST /documents/:id/redact's response shape — the
// redaction record plus the newly created derivative's own document
// summary (the exact same DocumentSummary shape upload/case-detail already
// return — never the raw redacted bytes, master prompt §22). The source
// document's own row/hash/object/certificate are completely unaffected by
// this call and remain independently readable exactly as before, at their
// own, unchanged document ID.
type RedactionSummary struct {
	RedactionID      uuid.UUID       `json:"redaction_id"`
	SourceDocumentID uuid.UUID       `json:"source_document_id"`
	Reason           string          `json:"reason"`
	CreatedAt        time.Time       `json:"created_at"`
	Document         DocumentSummary `json:"document"`
}

// RedactDocument authorizes user for document:redact on documentID (RBAC
// permission AND the document's case relationship — see
// authz.Service.CanAccessDocument), then:
//
//  1. Loads the source document's metadata under the caller's own RLS
//     identity (never its bytes yet).
//  2. Refuses (utils.ErrUnprocessableEntity) unless the source's stored
//     mime_type is one of supportedRedactionFormats.
//  3. Retrieves the source's actual stored object and recomputes its
//     SHA-256, refusing (utils.ErrConflict) if it no longer matches the
//     canonical documents.sha256_hash — the same anti-tamper check
//     CertificateService.generateCertificate performs before issuing a
//     certificate (shared via reconcileTamperStatus): deriving a
//     "redacted" copy from bytes that don't match what was actually
//     ingested would silently launder a tampering event into a
//     brand-new, seemingly-clean document.
//  4. Decodes the verified bytes as an image, validates every requested
//     region against the image's REAL dimensions, and destructively
//     overwrites each region's pixels (opaque black, draw.Src — never an
//     overlay) on an in-memory copy.
//  5. Re-encodes the redacted image in the SAME format, computes its
//     SHA-256 (H2) — never trusting/accepting a client-supplied hash —
//     and uploads it to object storage under a brand-new object key
//     derived from a brand-new document ID (documentObjectKey, the exact
//     same helper/convention UploadDocument uses).
//  6. In one transaction, inserts the derivative's documents row
//     (parent_document_id = the source's ID) and its redactions row
//     (source_document_id/result_document_id/region_data/reason/
//     created_by). A storage write that succeeds followed by a failed
//     transaction triggers the same best-effort orphan-object cleanup
//     UploadDocument uses.
//
// The source document's row, object, sha256_hash, and any existing
// compliance certificate are NEVER read-modify-written by this method —
// only ever read. H1 (the source's hash) is therefore provably unchanged
// by construction, not merely "not intended to change".
func (s *DocumentService) RedactDocument(ctx context.Context, user auth.AuthenticatedUser, documentID uuid.UUID, input RedactDocumentInput) (*RedactionSummary, error) {
	decision, err := s.authz.CanAccessDocument(ctx, user, documentID, authz.ActionDocumentRedact)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !decision.Allowed {
		return nil, utils.ErrForbidden(genericDocumentForbiddenMessage)
	}

	reason, err := validateRedactionReason(input.Reason)
	if err != nil {
		return nil, err
	}
	regions, err := validateRedactionRegions(input.Regions)
	if err != nil {
		return nil, err
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
			// Already authorized above — a row that vanishes between that
			// check and this read is the same anti-enumeration posture as
			// "not found", never a distinguishable 404.
			return nil, utils.ErrForbidden(genericDocumentForbiddenMessage)
		}
		return nil, utils.ErrInternal(err)
	}

	imageFormat, supported := supportedRedactionFormats[doc.MimeType]
	if !supported {
		return nil, utils.ErrUnprocessableEntity(fmt.Sprintf(
			"Redaction is not supported for this document's file type (%s). Supported formats: image/png, image/jpeg.", doc.MimeType))
	}

	originalBytes, err := readAllLimited(ctx, s.storage, doc.StorageObjectKey, s.maxUploadSize)
	if err != nil {
		s.logger.ErrorContext(ctx, "redaction: could not retrieve source object from storage",
			slog.String("document_id", doc.ID.String()),
			slog.String("case_id", doc.CaseID.String()),
			slog.String("storage_object_key", doc.StorageObjectKey),
			slog.String("error", err.Error()),
		)
		return nil, utils.ErrServiceUnavailable("The document could not be retrieved for redaction")
	}

	computedHash := sha256Sum(originalBytes)
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
				"context":       "redaction",
			},
		})
		return nil, utils.ErrConflict("Cannot redact: the source document failed integrity verification")
	}

	decodedImg, decodedFormat, err := image.Decode(bytes.NewReader(originalBytes))
	if err != nil {
		return nil, utils.ErrUnprocessableEntity("The document's content could not be decoded as a valid image")
	}
	if decodedFormat != imageFormat {
		// The content-sniffed mime_type recorded at upload disagrees with
		// what the bytes actually decode as — refuse rather than guess
		// which one to trust.
		return nil, utils.ErrUnprocessableEntity("The document's actual content does not match its recorded file type")
	}

	bounds := decodedImg.Bounds()
	for _, r := range regions {
		if r.Page != 1 {
			return nil, utils.ErrBadRequest("page must be 1 for this document's file type")
		}
		if r.X < float64(bounds.Min.X) || r.Y < float64(bounds.Min.Y) ||
			r.X+r.Width > float64(bounds.Max.X) || r.Y+r.Height > float64(bounds.Max.Y) {
			return nil, utils.ErrBadRequest("a redaction region falls outside the document's image bounds")
		}
	}

	redacted := applyRedactions(decodedImg, regions)

	var buf bytes.Buffer
	switch imageFormat {
	case "png":
		err = png.Encode(&buf, redacted)
	case "jpeg":
		err = jpeg.Encode(&buf, redacted, &jpeg.Options{Quality: 95})
	}
	if err != nil {
		return nil, utils.ErrInternal(fmt.Errorf("encode redacted image: %w", err))
	}

	derivativeHash := sha256Sum(buf.Bytes())
	if bytes.Equal(derivativeHash, doc.Sha256Hash) {
		// Not reachable in practice given regions are validated non-empty
		// with positive area above, but master prompt §11 is explicit:
		// never assume a byte-identical "derivative" is valid — refuse
		// instead of silently persisting one indistinguishable from its
		// source.
		return nil, utils.ErrInternal(errors.New("redaction did not change the document's content"))
	}

	regionData, err := json.Marshal(regions)
	if err != nil {
		return nil, utils.ErrInternal(fmt.Errorf("marshal region data: %w", err))
	}

	derivativeID := uuid.New()
	objectKey := documentObjectKey(doc.CaseID, derivativeID)
	if err := s.storage.Put(ctx, objectKey, bytes.NewReader(buf.Bytes()), int64(buf.Len()), doc.MimeType); err != nil {
		return nil, utils.ErrInternal(fmt.Errorf("store redacted derivative: %w", err))
	}

	filename := redactedFilename(doc.Filename)

	var created generated.Document
	var redactionRow generated.Redaction
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		repo := repository.NewDocumentRepo(q)
		d, err := repo.Create(ctx, generated.CreateDocumentParams{
			ID:               derivativeID,
			CaseID:           doc.CaseID,
			ParentDocumentID: &doc.ID,
			DocumentType:     doc.DocumentType,
			Filename:         filename,
			Description:      doc.Description,
			MimeType:         doc.MimeType,
			FileSize:         int64(buf.Len()),
			Sha256Hash:       derivativeHash,
			StorageBucket:    s.bucket,
			StorageObjectKey: objectKey,
			Metadata:         []byte(`{}`),
			UploadedBy:       user.ID, // server-controlled — never client-supplied
		})
		if err != nil {
			return fmt.Errorf("create derivative document: %w", err)
		}
		created = d

		red, err := repo.CreateRedaction(ctx, generated.CreateRedactionParams{
			SourceDocumentID: doc.ID,
			ResultDocumentID: derivativeID,
			RegionData:       regionData,
			Reason:           &reason,
			CreatedBy:        user.ID, // server-controlled — never client-supplied
		})
		if err != nil {
			return fmt.Errorf("create redaction record: %w", err)
		}
		redactionRow = red
		return nil
	})
	if err != nil {
		s.cleanupOrphan(ctx, objectKey, doc.CaseID, derivativeID, "postgresql insert failed after redacted derivative object storage succeeded")
		return nil, utils.ErrInternal(fmt.Errorf("persist redaction: %w", err))
	}

	s.recorder.Record(ctx, audit.Event{
		Action:       "DOCUMENT_REDACTED",
		ResourceType: "document",
		ResourceID:   &created.ID,
		UserID:       &user.ID,
		Role:         role,
		CaseID:       &doc.CaseID,
		Metadata: map[string]any{
			"source_document_id": doc.ID.String(),
			"result_document_id": created.ID.String(),
			"reason":             reason,
			"region_count":       len(regions),
			"source_sha256_hash": hex.EncodeToString(doc.Sha256Hash),
			"result_sha256_hash": hex.EncodeToString(created.Sha256Hash),
		},
	})

	return &RedactionSummary{
		RedactionID:      redactionRow.ID,
		SourceDocumentID: doc.ID,
		Reason:           reason,
		CreatedAt:        redactionRow.CreatedAt,
		Document:         toDocumentSummary(created),
	}, nil
}

// validateRedactionReason trims and bounds-checks the client-supplied
// redaction reason. A redaction is a deliberate, legally-accountable act
// (master prompt §20) — it must always carry a real, human-readable
// justification, never an empty string silently defaulted.
func validateRedactionReason(raw string) (string, error) {
	reason := strings.TrimSpace(raw)
	if len(reason) < minRedactionReasonLen {
		return "", utils.ErrBadRequest("reason is required and must describe why this redaction is being made")
	}
	if len(reason) > maxRedactionReasonLen {
		return "", utils.ErrBadRequest(fmt.Sprintf("reason must be at most %d characters", maxRedactionReasonLen))
	}
	if !utf8.ValidString(reason) {
		return "", utils.ErrBadRequest("reason must be valid UTF-8")
	}
	return reason, nil
}

// validateRedactionRegions performs structural validation independent of
// any specific document's real dimensions (checked separately once the
// source image is decoded — see RedactDocument) — request shape only:
// at least one region, not too many, and every coordinate/dimension
// finite, non-negative, and positive-area. encoding/json cannot itself
// produce NaN/Infinity from request JSON (neither literal is valid JSON
// syntax), but the explicit check costs nothing and documents the
// invariant for any future non-JSON caller (master prompt §7).
func validateRedactionRegions(regions []RedactRegion) ([]RedactRegion, error) {
	if len(regions) == 0 {
		return nil, utils.ErrBadRequest("at least one redaction region is required")
	}
	if len(regions) > maxRedactionRegions {
		return nil, utils.ErrBadRequest(fmt.Sprintf("at most %d redaction regions are allowed per request", maxRedactionRegions))
	}
	for _, r := range regions {
		if math.IsNaN(r.X) || math.IsNaN(r.Y) || math.IsNaN(r.Width) || math.IsNaN(r.Height) ||
			math.IsInf(r.X, 0) || math.IsInf(r.Y, 0) || math.IsInf(r.Width, 0) || math.IsInf(r.Height, 0) {
			return nil, utils.ErrBadRequest("redaction region coordinates must be finite numbers")
		}
		if r.Page < 1 {
			return nil, utils.ErrBadRequest("redaction region page must be 1 or greater")
		}
		if r.X < 0 || r.Y < 0 {
			return nil, utils.ErrBadRequest("redaction region coordinates must not be negative")
		}
		if r.Width <= 0 || r.Height <= 0 {
			return nil, utils.ErrBadRequest("redaction region width/height must be positive")
		}
	}
	return regions, nil
}

// applyRedactions returns a NEW, independent image with every region in
// regions destructively overwritten with opaque black — draw.Draw with
// draw.Src performs a straight pixel REPLACE, never an alpha-blended
// overlay, so the source pixel values genuinely no longer exist anywhere
// in the returned image. img itself is never mutated. Each region's
// bounds are rounded OUTWARD (floor the min corner, ceil the max corner)
// so the mask always fully covers the requested area — truncating inward
// could otherwise leave a one-pixel sliver of the "redacted" content
// visible at an edge, defeating the entire point (master prompt §9).
func applyRedactions(img image.Image, regions []RedactRegion) draw.Image {
	bounds := img.Bounds()
	dst := image.NewNRGBA(bounds)
	draw.Draw(dst, bounds, img, bounds.Min, draw.Src)

	black := image.NewUniform(color.NRGBA{R: 0, G: 0, B: 0, A: 255})
	for _, r := range regions {
		rect := image.Rect(
			bounds.Min.X+int(math.Floor(r.X)),
			bounds.Min.Y+int(math.Floor(r.Y)),
			bounds.Min.X+int(math.Ceil(r.X+r.Width)),
			bounds.Min.Y+int(math.Ceil(r.Y+r.Height)),
		).Intersect(bounds)
		draw.Draw(dst, rect, black, image.Point{}, draw.Src)
	}
	return dst
}

// sha256Sum computes data's SHA-256 digest in one shot — used here (rather
// than the streaming io.TeeReader pattern streamToStorage uses for
// uploads) because redaction must decode/re-encode the whole image in
// memory regardless, bounded by maxUploadSize via readAllLimited, so there
// is no separate streaming pass to tee into.
func sha256Sum(data []byte) []byte {
	h := hash.New()
	h.Write(data)
	return h.Sum(nil)
}

// readAllLimited retrieves the object at key and reads it fully into
// memory, capped at limit+1 bytes so an oversized object is rejected
// (never fully buffered) rather than exhausting process memory — the same
// bound (DocumentsConfig.MaxUploadSize) uploads themselves are held to,
// applied symmetrically on the read side for redaction's decode/re-encode
// pipeline (master prompt §29/§40).
func readAllLimited(ctx context.Context, objStorage storage.Storage, key string, limit int64) ([]byte, error) {
	reader, err := objStorage.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("retrieve stored object: %w", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read stored object: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("stored object exceeds maximum processing size of %d bytes", limit)
	}
	return data, nil
}

// redactedFilename derives the derivative's display filename from the
// source's (itself already sanitized at upload time — see
// DocumentService.sanitizeFilename — so no further path/control-character
// stripping is needed here), bounded to the same maxDocumentFilenameLen
// every document filename respects.
func redactedFilename(original string) string {
	name := "redacted_" + original
	if len(name) > maxDocumentFilenameLen {
		name = name[:maxDocumentFilenameLen]
	}
	return name
}
