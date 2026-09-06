// Package service (this file): secure document sharing & access
// delegation. A share grants ONE recipient a SPECIFIC, revocable,
// optionally time-bounded permission (VIEW or VERIFY) on ONE document —
// never ownership, never resharing, never redaction, never access to any
// other document (including the shared document's own source/derivatives
// — see docs/SECURITY.md's "Document Sharing" for the lineage rule).
//
// Reuses Systems 1-8 entirely: authorization goes through the same
// authz.Service.CanAccessDocument every other document route already
// calls (ActionDocumentShare to create/list/revoke a share; the
// delegated-access path it now also implements, in share_policy.go, is
// what makes a granted share actually usable on
// download/verify/certificate — no change was needed to those routes'
// own handlers or services). Audit goes through the same
// internal/audit.Recorder. No new identifier strategy, no new response
// envelope, no new authentication mechanism.
package service

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

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
	"evidentia/backend/internal/utils"
)

const (
	maxShareReasonLen = 2000
	// minRecipientSearchLen guards SearchRecipients against becoming an
	// unrestricted user directory (master prompt §48): a bare/near-empty
	// query is refused rather than returning an arbitrary page of every
	// active user.
	minRecipientSearchLen = 2
	maxRecipientSearchLen = 255
	// maxRecipientResults caps SearchRecipients regardless of how many
	// users match — master prompt §48's "use reasonable query limits",
	// independent of the general list-pagination MaxPageSize (this is a
	// type-ahead selector, not a paginated listing).
	maxRecipientResults = 10
)

var sharePermissions = map[string]bool{
	models.SharePermissionView:   true,
	models.SharePermissionVerify: true,
}

// CreateShareInput is CreateShare's request shape.
type CreateShareInput struct {
	RecipientUserID uuid.UUID
	Permission      string
	ExpiresAt       *time.Time
	Reason          *string
}

// ShareSummary is a document_shares row shaped for API responses — never
// the raw generated.DocumentShare (whose json tags are fine as-is here,
// but this type additionally carries the computed EffectiveStatus, which
// has no column of its own — see models.ShareEffectiveStatus*).
type ShareSummary struct {
	ShareID         uuid.UUID  `json:"share_id"`
	DocumentID      uuid.UUID  `json:"document_id"`
	RecipientUserID uuid.UUID  `json:"recipient_user_id"`
	CreatedByUserID uuid.UUID  `json:"created_by_user_id"`
	Permission      string     `json:"permission"`
	Status          string     `json:"status"`
	EffectiveStatus string     `json:"effective_status"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	Reason          *string    `json:"reason,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	RevokedAt       *time.Time `json:"revoked_at,omitempty"`
	RevokedByUserID *uuid.UUID `json:"revoked_by_user_id,omitempty"`
}

// SharedDocumentSummary is one row of GET /shared/documents ("Shared
// With Me" — master prompt §59). Document reuses the exact same
// DocumentSummary shape upload/case-detail/redact already return.
type SharedDocumentSummary struct {
	ShareID        uuid.UUID       `json:"share_id"`
	Permission     string          `json:"permission"`
	ExpiresAt      *time.Time      `json:"expires_at,omitempty"`
	SharedAt       time.Time       `json:"shared_at"`
	SharedByUserID uuid.UUID       `json:"shared_by_user_id"`
	Document       DocumentSummary `json:"document"`
}

// SharedWithMeResult is GET /shared/documents's response data.
type SharedWithMeResult struct {
	Documents []SharedDocumentSummary `json:"documents"`
	Meta      utils.Meta              `json:"meta"`
}

// RecipientCandidate is one SearchRecipients result — deliberately a
// small, safe subset of a user's fields (master prompt §38/§48): no
// phone, status, timestamps, or any field beyond what a recipient picker
// needs to display.
type RecipientCandidate struct {
	ID          uuid.UUID `json:"id"`
	FirstName   string    `json:"first_name"`
	LastName    string    `json:"last_name"`
	DisplayName *string   `json:"display_name,omitempty"`
	Email       string    `json:"email"`
	Roles       []string  `json:"roles"`
}

// ShareService owns document-sharing business logic: authorization
// (delegated entirely to authz.Service.CanAccessDocument with
// authz.ActionDocumentShare — never a hand-rolled role check),
// recipient validation, share creation/listing/revocation, the "Shared
// With Me" listing, and recipient search. It never touches object
// storage or document bytes — sharing changes only access metadata,
// never document content, hash, or certificates (master prompt §21).
type ShareService struct {
	pool      *pgxpool.Pool
	authz     *authz.Service
	recorder  audit.Recorder
	publisher events.Publisher
}

func NewShareService(pool *pgxpool.Pool, authzService *authz.Service, recorder audit.Recorder, publisher events.Publisher) *ShareService {
	return &ShareService{pool: pool, authz: authzService, recorder: recorder, publisher: publisher}
}

// CreateShare authorizes user for document:share on documentID (RBAC
// permission AND the document's case relationship — see
// authz.Service.CanAccessDocument; note that a delegated share can NEVER
// itself satisfy this check — ActionDocumentShare is not in
// shareViewActions/shareVerifyActions, so a recipient can never reshare
// what was shared with them, master prompt §7), validates the request,
// verifies the recipient is a real, active, distinct Evidentia user, and
// persists a new ACTIVE share row. A duplicate active share for the same
// (document, recipient) pair is rejected with a 409 — the database's own
// document_shares_active_unique index is the actual enforcement (master
// prompt §31), this is only where that violation is translated into a
// safe client-facing error.
func (s *ShareService) CreateShare(ctx context.Context, user auth.AuthenticatedUser, documentID uuid.UUID, input CreateShareInput) (*ShareSummary, error) {
	decision, err := s.authz.CanAccessDocument(ctx, user, documentID, authz.ActionDocumentShare)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !decision.Allowed {
		return nil, utils.ErrForbidden(genericDocumentForbiddenMessage)
	}

	if !sharePermissions[input.Permission] {
		return nil, utils.ErrBadRequest("permission must be VIEW or VERIFY")
	}
	if input.Reason != nil && len(*input.Reason) > maxShareReasonLen {
		return nil, utils.ErrBadRequest(fmt.Sprintf("reason must be at most %d characters", maxShareReasonLen))
	}
	if input.ExpiresAt != nil && !input.ExpiresAt.After(time.Now()) {
		return nil, utils.ErrBadRequest("expires_at must be in the future")
	}
	// Master prompt §33: reject self-share outright (also enforced at
	// the database level by document_shares_not_self_check — this check
	// exists purely to return a clear 400 instead of a raw constraint
	// error).
	if input.RecipientUserID == user.ID {
		return nil, utils.ErrBadRequest("cannot share a document with yourself")
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

	// Recipient validation (master prompt §17): must exist and be
	// active. A single generic message covers both "does not exist" and
	// "inactive" — the same anti-enumeration posture this codebase
	// already applies to document/case IDOR responses, applied here to
	// user IDs.
	if err := s.validateRecipient(ctx, input.RecipientUserID); err != nil {
		return nil, err
	}

	shareID := uuid.New()
	var created generated.DocumentShare
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		c, err := repository.NewShareRepo(q).Create(ctx, generated.CreateDocumentShareParams{
			ID:               shareID,
			DocumentID:       documentID,
			SharedWithUserID: input.RecipientUserID,
			CreatedByUserID:  user.ID, // server-controlled — never client-supplied
			Permission:       input.Permission,
			ExpiresAt:        toTimestamptz(input.ExpiresAt),
			Reason:           input.Reason,
		})
		created = c
		return err
	})
	if err != nil {
		if isUniqueViolation(err, "document_shares_active_unique") {
			return nil, utils.ErrConflict("An active share already exists for this recipient on this document")
		}
		return nil, utils.ErrInternal(fmt.Errorf("create document share: %w", err))
	}

	role := effectiveCaseRole(user)
	s.recorder.Record(ctx, audit.Event{
		Action:       "DOCUMENT_SHARED",
		ResourceType: "document_share",
		ResourceID:   &created.ID,
		UserID:       &user.ID,
		Role:         role,
		CaseID:       &doc.CaseID,
		Metadata: map[string]any{
			"document_id":       documentID.String(),
			"recipient_user_id": input.RecipientUserID.String(),
			"permission":        input.Permission,
			"has_expiration":    input.ExpiresAt != nil,
		},
	})
	s.publisher.Publish(ctx, events.TypeShareCreated, events.ResourceTypeCase, doc.CaseID.String(), events.ShareEventData{
		ShareID: created.ID.String(), DocumentID: documentID.String(), CaseID: doc.CaseID.String(),
	})

	summary := toShareSummary(created)
	return &summary, nil
}

// ListShares authorizes user for document:share on documentID (the same
// authority required to CREATE a share also governs who may see its
// share list — master prompt §9), then returns every share ever created
// for it, newest first, including revoked ones (historical delegation
// records are never hidden, only their EffectiveStatus changes).
func (s *ShareService) ListShares(ctx context.Context, user auth.AuthenticatedUser, documentID uuid.UUID) ([]ShareSummary, error) {
	decision, err := s.authz.CanAccessDocument(ctx, user, documentID, authz.ActionDocumentShare)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !decision.Allowed {
		return nil, utils.ErrForbidden(genericDocumentForbiddenMessage)
	}

	ident := repository.AppIdentity{UserID: user.ID, Role: effectiveCaseRole(user)}
	var rows []generated.DocumentShare
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		r, err := repository.NewShareRepo(q).ListForDocument(ctx, documentID)
		rows = r
		return err
	})
	if err != nil {
		return nil, utils.ErrInternal(err)
	}

	summaries := make([]ShareSummary, len(rows))
	for i, r := range rows {
		summaries[i] = toShareSummary(r)
	}
	return summaries, nil
}

// RevokeShare authorizes user for document:share on documentID (same
// authority as creating a share — "authorized to manage this document's
// sharing", not narrowed to only the original creator; see
// db/migrations/000004_document_sharing.up.sql's document_shares_update
// policy comment for the identical RLS-level rule), then transitions the
// named share from ACTIVE to REVOKED. shareID is looked up scoped to
// BOTH its own id AND documentID (master prompt §16/§50's "cannot use
// another share ID" — a share ID that is real but belongs to a DIFFERENT
// document is treated identically to a nonexistent one, never leaking
// which case it actually belongs to). Revoking an already-revoked (or
// nonexistent, or cross-document) share both return the same generic
// not-found error — no distinguishable response tells a caller which.
func (s *ShareService) RevokeShare(ctx context.Context, user auth.AuthenticatedUser, documentID, shareID uuid.UUID) (*ShareSummary, error) {
	decision, err := s.authz.CanAccessDocument(ctx, user, documentID, authz.ActionDocumentShare)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !decision.Allowed {
		return nil, utils.ErrForbidden(genericDocumentForbiddenMessage)
	}

	ident := repository.AppIdentity{UserID: user.ID, Role: effectiveCaseRole(user)}
	var revoked generated.DocumentShare
	var doc generated.Document
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		r, err := repository.NewShareRepo(q).Revoke(ctx, shareID, documentID, user.ID)
		if err != nil {
			return err
		}
		revoked = r
		// Read-only, same transaction — needed only to scope the
		// SHARE_REVOKED notification below to the document's case (see
		// events.ResourceTypeCase); never fails this operation over a
		// notification concern, since a NotFound here would already have
		// been caught above via decision.Allowed for the SAME document.
		d, err := repository.NewDocumentRepo(q).GetByID(ctx, documentID)
		doc = d
		return err
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, utils.ErrNotFound("Share not found or already revoked")
		}
		return nil, utils.ErrInternal(fmt.Errorf("revoke document share: %w", err))
	}

	s.recorder.Record(ctx, audit.Event{
		Action:       "DOCUMENT_SHARE_REVOKED",
		ResourceType: "document_share",
		ResourceID:   &revoked.ID,
		UserID:       &user.ID,
		Role:         effectiveCaseRole(user),
		Metadata: map[string]any{
			"document_id":       documentID.String(),
			"recipient_user_id": revoked.SharedWithUserID.String(),
		},
	})
	s.publisher.Publish(ctx, events.TypeShareRevoked, events.ResourceTypeCase, doc.CaseID.String(), events.ShareEventData{
		ShareID: revoked.ID.String(), DocumentID: documentID.String(), CaseID: doc.CaseID.String(),
	})

	summary := toShareSummary(revoked)
	return &summary, nil
}

// ListSharedWithMe returns every document currently, actively shared with
// user — master prompt §59's "Shared With Me". No separate
// CanAccessDocument check is needed per row: RLS's documents_select
// policy (and this query's own WHERE clause) already restrict results to
// exactly the caller's own valid, unexpired shares — there is nothing
// else to authorize.
func (s *ShareService) ListSharedWithMe(ctx context.Context, user auth.AuthenticatedUser, page utils.Pagination) (*SharedWithMeResult, error) {
	ident := repository.AppIdentity{UserID: user.ID, Role: effectiveCaseRole(user)}
	var rows []generated.ListSharedWithMeRow
	var total int64
	err := repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		repo := repository.NewShareRepo(q)
		r, err := repo.ListSharedWithMe(ctx, user.ID, page.Limit(), page.Offset())
		if err != nil {
			return err
		}
		rows = r
		total, err = repo.CountSharedWithMe(ctx, user.ID)
		return err
	})
	if err != nil {
		return nil, utils.ErrInternal(err)
	}

	docs := make([]SharedDocumentSummary, len(rows))
	for i, r := range rows {
		docs[i] = SharedDocumentSummary{
			ShareID:        r.ShareID,
			Permission:     r.Permission,
			ExpiresAt:      timestamptzPtr(r.ExpiresAt),
			SharedAt:       r.ShareCreatedAt,
			SharedByUserID: r.CreatedByUserID,
			Document: DocumentSummary{
				ID:               r.DocumentID,
				CaseID:           r.CaseID,
				DocumentType:     r.DocumentType,
				Filename:         r.Filename,
				Description:      r.Description,
				MimeType:         r.MimeType,
				FileSize:         r.FileSize,
				Sha256Hash:       hexEncodeHash(r.Sha256Hash),
				Status:           r.DocumentStatus,
				ParentDocumentID: r.ParentDocumentID,
				UploadedBy:       r.UploadedBy,
				UploadedAt:       r.UploadedAt,
			},
		}
	}

	return &SharedWithMeResult{Documents: docs, Meta: page.BuildMeta(total)}, nil
}

// SearchRecipients returns up to maxRecipientResults ACTIVE users whose
// email/name matches query (case-insensitive substring) — the
// share-recipient picker's data source (master prompt §38/§48). Requires
// only authentication, deliberately NOT authz.ActionUserRead (that
// remains ADMIN-only global user management, a materially different,
// more sensitive capability — see UserService.ListUsers's own doc
// comment): this returns a small, safe field subset, requires a real
// query, excludes inactive users and the caller themselves, and is
// capped well below the general list page size, so it cannot become a
// full-directory enumeration tool.
func (s *ShareService) SearchRecipients(ctx context.Context, user auth.AuthenticatedUser, query string) ([]RecipientCandidate, error) {
	q := strings.TrimSpace(query)
	if len(q) < minRecipientSearchLen {
		return nil, utils.ErrBadRequest(fmt.Sprintf("search query must be at least %d characters", minRecipientSearchLen))
	}
	if len(q) > maxRecipientSearchLen {
		return nil, utils.ErrBadRequest("search query is too long")
	}

	active := models.UserStatusActive
	ident := repository.AppIdentity{UserID: user.ID, Role: effectiveCaseRole(user)}
	var rows []generated.ListUsersFilteredRow
	err := repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q2 *generated.Queries) error {
		r, err := q2.ListUsersFiltered(ctx, generated.ListUsersFilteredParams{
			Status:    &active,
			Search:    &q,
			OffsetVal: 0,
			LimitVal:  maxRecipientResults,
		})
		rows = r
		return err
	})
	if err != nil {
		return nil, utils.ErrInternal(err)
	}

	candidates := make([]RecipientCandidate, 0, len(rows))
	for _, r := range rows {
		if r.ID == user.ID {
			continue // never suggest sharing with yourself
		}
		roles, err := s.rolesForUser(ctx, ident, r.ID)
		if err != nil {
			return nil, utils.ErrInternal(err)
		}
		candidates = append(candidates, RecipientCandidate{
			ID:          r.ID,
			FirstName:   r.FirstName,
			LastName:    r.LastName,
			DisplayName: r.DisplayName,
			Email:       r.Email,
			Roles:       roles,
		})
	}
	return candidates, nil
}

func (s *ShareService) rolesForUser(ctx context.Context, ident repository.AppIdentity, userID uuid.UUID) ([]string, error) {
	var names []string
	err := repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		roles, err := q.ListRolesForUser(ctx, userID)
		if err != nil {
			return err
		}
		names = roleNames(roles)
		return nil
	})
	return names, err
}

// validateRecipient confirms recipientID is a real, currently-active
// Evidentia user (master prompt §17). Deliberately a single generic
// error for both "no such user" and "inactive" — the same
// non-enumerating posture this codebase already applies to document/case
// IDOR responses.
func (s *ShareService) validateRecipient(ctx context.Context, recipientID uuid.UUID) error {
	var profile generated.GetUserByIDRow
	err := repository.WithTx(ctx, s.pool, repository.AppIdentity{}, func(ctx context.Context, q *generated.Queries) error {
		p, err := q.GetUserByID(ctx, recipientID)
		profile = p
		return err
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return utils.ErrBadRequest("Invalid or inactive recipient")
		}
		return utils.ErrInternal(err)
	}
	if profile.Status != models.UserStatusActive {
		return utils.ErrBadRequest("Invalid or inactive recipient")
	}
	return nil
}

// hexEncodeHash hex-encodes a raw SHA-256 digest — the same conversion
// toDocumentSummary performs, duplicated here (rather than reusing that
// function directly) because ListSharedWithMeRow's field set is shaped
// by a JOIN, not a plain generated.Document.
func hexEncodeHash(sum []byte) string {
	return hex.EncodeToString(sum)
}

// toShareSummary computes EffectiveStatus (master prompt §11) from the
// stored status + expires_at — never persisted, always derived fresh, so
// it can never drift from what documents_select's RLS branch and
// share_policy.go's shareGrantsAccess independently compute the same way.
func toShareSummary(s generated.DocumentShare) ShareSummary {
	effective := s.Status
	if s.Status == models.ShareStatusActive && s.ExpiresAt.Valid && !s.ExpiresAt.Time.After(time.Now()) {
		effective = models.ShareEffectiveStatusExpired
	}

	return ShareSummary{
		ShareID:         s.ID,
		DocumentID:      s.DocumentID,
		RecipientUserID: s.SharedWithUserID,
		CreatedByUserID: s.CreatedByUserID,
		Permission:      s.Permission,
		Status:          s.Status,
		EffectiveStatus: effective,
		ExpiresAt:       timestamptzPtr(s.ExpiresAt),
		Reason:          s.Reason,
		CreatedAt:       s.CreatedAt,
		RevokedAt:       timestamptzPtr(s.RevokedAt),
		RevokedByUserID: s.RevokedByUserID,
	}
}
