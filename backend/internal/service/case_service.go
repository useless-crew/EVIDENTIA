package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"evidentia/backend/db/generated"
	"evidentia/backend/internal/audit"
	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/authz"
	"evidentia/backend/internal/models"
	"evidentia/backend/internal/repository"
	"evidentia/backend/internal/utils"
)

// genericCaseForbiddenMessage matches internal/middleware's genericForbiddenMessage
// verbatim (that constant is unexported, so it cannot be imported directly)
// — CaseService independently re-checks authorization (see this file's doc
// comment) and must produce the identical client-facing response the
// middleware already would, so a caller can never distinguish "denied by
// the middleware" from "denied by the service" (master prompt §21/§25: no
// distinguishable response that could help enumerate resources).
const genericCaseForbiddenMessage = "You do not have permission to perform this action"

// maxCaseDetailDocuments/maxCaseDetailInvolvedParties cap how many
// documents/involved-party rows a single GET /cases/:id response embeds.
// Full, independently paginated listing of either is a later system's
// concern (document listing: System 6) — this is a case-detail
// convenience, not this system's primary access path to either resource.
const (
	maxCaseDetailDocuments       = 50
	maxCaseDetailInvolvedParties = 200
)

// caseStatuses is the exact set of values cases_status_check allows (see
// db/migrations/000001_init_schema.up.sql) — mirrored here, not
// reinvented, so status validation can never drift from what the database
// itself accepts.
var caseStatuses = map[string]bool{
	models.CaseStatusOpen:               true,
	models.CaseStatusUnderInvestigation: true,
	models.CaseStatusSubmitted:          true,
	models.CaseStatusUnderReview:        true,
	models.CaseStatusClosed:             true,
	models.CaseStatusArchived:           true,
}

// caseStatusTransitions is System 5's OWN validated status-transition
// model — System 2's schema only constrains status to the fixed value set
// above, it does not encode a transition graph, and no other part of this
// repository defines one. This is a deliberately conservative, documented
// starting point (master prompt §18: "implement validated status
// assignment and document that richer workflow rules can be added later"),
// not a claim that this is the final investigative workflow:
//
//	OPEN -> UNDER_INVESTIGATION -> SUBMITTED -> UNDER_REVIEW -> CLOSED -> ARCHIVED
//
// with SUBMITTED/UNDER_REVIEW allowed to fall back a step (e.g. a review
// sends a case back for further investigation), and ARCHIVED reachable
// directly from any non-terminal state (an investigation can be shelved at
// any point) but itself terminal. Assigning a case's CURRENT status again
// (a no-op status-wise, e.g. updating only the title) is always allowed
// regardless of this map.
var caseStatusTransitions = map[string]map[string]bool{
	models.CaseStatusOpen: {
		models.CaseStatusUnderInvestigation: true,
		models.CaseStatusArchived:           true,
	},
	models.CaseStatusUnderInvestigation: {
		models.CaseStatusSubmitted: true,
		models.CaseStatusArchived:  true,
	},
	models.CaseStatusSubmitted: {
		models.CaseStatusUnderReview:        true,
		models.CaseStatusUnderInvestigation: true,
		models.CaseStatusArchived:           true,
	},
	models.CaseStatusUnderReview: {
		models.CaseStatusClosed:             true,
		models.CaseStatusUnderInvestigation: true,
		models.CaseStatusArchived:           true,
	},
	models.CaseStatusClosed: {
		models.CaseStatusArchived: true,
	},
	models.CaseStatusArchived: {},
}

const (
	maxCaseNumberLen = 100
	maxCaseTitleLen  = 255
	maxCaseDescLen   = 10_000
	maxSearchTermLen = 255
)

// ---- DTOs ----
//
// These are the ONLY case-shaped values this service returns to a
// handler — never a bare generated.Case/generated.Document/
// generated.CaseInvolvedParty, which would leak internal storage columns
// (documents.storage_object_key, sha256_hash, ...) or bypass protected-
// party sanitization (master prompt §40).

type CaseSummary struct {
	ID          uuid.UUID       `json:"id"`
	CaseNumber  string          `json:"case_number"`
	Title       string          `json:"title"`
	Description *string         `json:"description,omitempty"`
	Status      string          `json:"status"`
	Metadata    json.RawMessage `json:"metadata"`
	CreatedBy   uuid.UUID       `json:"created_by"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type InvolvedPartySummary struct {
	ID          uuid.UUID       `json:"id"`
	PartyType   string          `json:"party_type"`
	DisplayName string          `json:"display_name"`
	Metadata    json.RawMessage `json:"metadata"`
	CreatedAt   time.Time       `json:"created_at"`
}

// DocumentSummary is a document reference — metadata and references only
// (master prompt §22/§46: never file bytes, never storage_bucket/
// storage_object_key, which remain internal). Used both standalone
// (System 6's upload response) and embedded in case detail (System 5).
// Sha256Hash is the hex-encoded digest System 6 computed at ingestion —
// exposing it here is display only; comparing it against a freshly
// recomputed hash to verify integrity is System 7's job, not this type's.
type DocumentSummary struct {
	ID           uuid.UUID `json:"id"`
	CaseID       uuid.UUID `json:"case_id"`
	DocumentType string    `json:"document_type"`
	Filename     string    `json:"filename"`
	Description  *string   `json:"description,omitempty"`
	MimeType     string    `json:"mime_type"`
	FileSize     int64     `json:"file_size"`
	Sha256Hash   string    `json:"sha256_hash"`
	Status       string    `json:"status"`
	UploadedBy   uuid.UUID `json:"uploaded_by"`
	UploadedAt   time.Time `json:"uploaded_at"`
}

// TimelineEvent is one entry in a case's chronological timeline (master
// prompt §23). Synthesized from existing timestamped case-scoped rows
// (cases/documents/case_involved_parties) — NOT read from audit_log, which
// no system populates yet (audit.SlogRecorder writes to the operational
// log only; the durable, hash-chained table is System 8's job — see
// audit.Recorder's doc comment). This is deliberately NOT a second,
// competing audit system: it reads data these tables already store for
// their own reasons, computing no new persisted state of its own.
type TimelineEvent struct {
	Type      string     `json:"type"`
	Timestamp time.Time  `json:"timestamp"`
	Summary   string     `json:"summary"`
	RelatedID *uuid.UUID `json:"related_id,omitempty"`
}

const (
	TimelineEventCaseCreated        = "CASE_CREATED"
	TimelineEventCaseUpdated        = "CASE_UPDATED"
	TimelineEventDocumentAdded      = "DOCUMENT_ADDED"
	TimelineEventInvolvedPartyAdded = "INVOLVED_PARTY_ADDED"
)

// CaseRelationship tells the caller their OWN standing on this case — the
// same relationship CanAccessCase already evaluated to allow the request,
// surfaced back so a client can render "you are the owner"/"you are a
// LAWYER on this case" without an extra request.
type CaseRelationship struct {
	IsOwner        bool    `json:"is_owner"`
	IsMember       bool    `json:"is_member"`
	MembershipType *string `json:"membership_type,omitempty"`
}

// CaseDetail is GET /cases/:id's response shape — master prompt §13's
// full list: metadata, status, involved parties, document references,
// timeline, timestamps.
type CaseDetail struct {
	CaseSummary
	InvolvedParties []InvolvedPartySummary `json:"involved_parties"`
	Documents       []DocumentSummary      `json:"documents"`
	Timeline        []TimelineEvent        `json:"timeline"`
	Relationship    CaseRelationship       `json:"relationship"`
}

// CaseListFilter is GET /cases's optional, server-side filter set (master
// prompt §10) — every field nil means "no constraint on this field".
// Filtering is layered ON TOP OF, never instead of, RLS row visibility.
type CaseListFilter struct {
	Status      *string
	CaseNumber  *string
	Title       *string
	CreatedBy   *uuid.UUID
	CreatedFrom *time.Time
	CreatedTo   *time.Time
}

type CaseListResult struct {
	Cases []CaseSummary `json:"cases"`
	Meta  utils.Meta    `json:"meta"`
}

// CreateCaseInput is CreateCase's request shape. Deliberately has NO id/
// created_by/created_at/updated_at fields — those are server-controlled
// (master prompt §5) and this type structurally cannot carry a
// client-supplied value for any of them.
type CreateCaseInput struct {
	CaseNumber  string
	Title       string
	Description *string
	Status      *string // nil => models.CaseStatusOpen
	Metadata    json.RawMessage
}

// UpdateCaseInput is PUT /cases/:id's request shape — a full replacement
// of every mutable field (see db/queries/cases.sql's UpdateCase, which
// this project already defined as an unconditional 4-column SET; master
// prompt §17 preserves that existing contract rather than introducing
// PATCH semantics). id/created_by/created_at are, again, structurally
// absent.
type UpdateCaseInput struct {
	Title       string
	Description *string
	Status      string
	Metadata    json.RawMessage
}

// CaseService owns case business logic: input validation, status-
// transition rules, transactional persistence via CaseRepo, and audit
// integration. It independently re-checks authorization via authz.Service
// rather than trusting that a caller already passed through
// middleware.RequirePermission/RequireCaseAccess — the same "service-layer
// authorization" design docs/SECURITY.md documents for internal/authz
// itself: a future caller of this service that bypasses HTTP (a
// background job, another service) gets the identical authorization
// guarantees a request would.
type CaseService struct {
	pool     *pgxpool.Pool
	authz    *authz.Service
	recorder audit.Recorder
}

func NewCaseService(pool *pgxpool.Pool, authzService *authz.Service, recorder audit.Recorder) *CaseService {
	return &CaseService{pool: pool, authz: authzService, recorder: recorder}
}

// ---- Create ----

// CreateCase validates req, authorizes user for case:create, and inserts
// the new case plus its creator's OWNER case_members row in one
// transaction (mirroring backend/tests/db_rls_test.go's
// TestRLS_CaseCreatorCanBootstrapOwnMembership — the case's creator must
// have their own active membership row so later reads/updates resolve via
// the same case_members-based relationship every other case member uses,
// not a created_by-only special case forever). On success, records a
// CASE_CREATED audit event; on any failure the transaction rolls back and
// no audit event is ever recorded for it (master prompt §7/§25).
func (s *CaseService) CreateCase(ctx context.Context, user auth.AuthenticatedUser, req CreateCaseInput) (*CaseDetail, error) {
	allowed, err := s.authz.HasPermission(ctx, user, authz.ActionCaseCreate)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !allowed {
		return nil, utils.ErrForbidden(genericCaseForbiddenMessage)
	}

	caseNumber := req.CaseNumber
	title := req.Title
	status := models.CaseStatusOpen
	if req.Status != nil && *req.Status != "" {
		status = *req.Status
	}

	if err := validateCaseNumber(caseNumber); err != nil {
		return nil, utils.ErrBadRequest(err.Error())
	}
	if err := validateCaseTitle(title); err != nil {
		return nil, utils.ErrBadRequest(err.Error())
	}
	if err := validateCaseDescription(req.Description); err != nil {
		return nil, utils.ErrBadRequest(err.Error())
	}
	if !caseStatuses[status] {
		return nil, utils.ErrBadRequest("Invalid case status")
	}
	if err := utils.ValidateJSONMetadata(req.Metadata); err != nil {
		return nil, utils.ErrBadRequest(err.Error())
	}
	metadata := normalizeMetadata(req.Metadata)

	ident := repository.AppIdentity{UserID: user.ID, Role: effectiveCaseRole(user)}

	var created generated.Case
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		repo := repository.NewCaseRepo(q)

		c, err := repo.Create(ctx, generated.CreateCaseParams{
			CaseNumber:  caseNumber,
			Title:       title,
			Description: req.Description,
			CreatedBy:   user.ID, // server-controlled — never req/client-supplied
			Metadata:    metadata,
		})
		if err != nil {
			return err
		}

		// The status column defaults to OPEN at the schema level; an
		// explicit non-default initial status requires a second statement
		// since CreateCase's INSERT (db/queries/cases.sql) does not accept
		// one — see that query's own comment for why (System 2 did not
		// expose it there).
		if status != models.CaseStatusOpen {
			c, err = repo.Update(ctx, generated.UpdateCaseParams{
				ID:          c.ID,
				Title:       c.Title,
				Description: c.Description,
				Status:      status,
				Metadata:    c.Metadata,
			})
			if err != nil {
				return err
			}
		}

		_, err = repo.AddMember(ctx, generated.AddCaseMemberParams{
			CaseID:         c.ID,
			UserID:         user.ID,
			MembershipType: models.MembershipTypeOwner,
			AddedBy:        user.ID,
		})
		if err != nil {
			return err
		}

		created = c
		return nil
	})
	if err != nil {
		if isUniqueViolation(err, "cases_case_number_unique") {
			return nil, utils.ErrConflict("A case with this case number already exists")
		}
		return nil, utils.ErrInternal(err)
	}

	s.recorder.Record(ctx, audit.Event{
		Action:       "CASE_CREATED",
		ResourceType: "case",
		ResourceID:   &created.ID,
		UserID:       &user.ID,
		Role:         effectiveCaseRole(user),
		CaseID:       &created.ID,
		Metadata:     map[string]any{"case_number": created.CaseNumber, "status": created.Status},
	})

	return &CaseDetail{
		CaseSummary:     toCaseSummary(created),
		InvolvedParties: []InvolvedPartySummary{},
		Documents:       []DocumentSummary{},
		Timeline:        buildTimeline(created, nil, nil),
		Relationship:    CaseRelationship{IsOwner: true, IsMember: true, MembershipType: strPtr(models.MembershipTypeOwner)},
	}, nil
}

// ---- List ----

// ListCases returns the caller's authorized, filtered, paginated case
// list. Role-scoping happens entirely at the database layer: RLS's
// cases_select policy (ADMIN sees all; every other caller sees cases they
// created or hold an active case_members row for) already restricts what
// ListCasesFiltered can return before filter/pagination are even applied
// — this method adds no additional Go-side filtering on top (master
// prompt §8/§29: never "SELECT all + filter in Go").
func (s *CaseService) ListCases(ctx context.Context, user auth.AuthenticatedUser, filter CaseListFilter, page utils.Pagination) (*CaseListResult, error) {
	allowed, err := s.authz.HasPermission(ctx, user, authz.ActionCaseRead)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !allowed {
		return nil, utils.ErrForbidden(genericCaseForbiddenMessage)
	}

	if filter.Status != nil && !caseStatuses[*filter.Status] {
		return nil, utils.ErrBadRequest("Invalid case status filter")
	}
	if filter.CaseNumber != nil && len(*filter.CaseNumber) > maxSearchTermLen {
		return nil, utils.ErrBadRequest("case_number filter is too long")
	}
	if filter.Title != nil && len(*filter.Title) > maxSearchTermLen {
		return nil, utils.ErrBadRequest("title filter is too long")
	}
	if filter.CreatedFrom != nil && filter.CreatedTo != nil && filter.CreatedTo.Before(*filter.CreatedFrom) {
		return nil, utils.ErrBadRequest("created_to must not be before created_from")
	}

	ident := repository.AppIdentity{UserID: user.ID, Role: effectiveCaseRole(user)}

	listArg := generated.ListCasesFilteredParams{
		Status:      filter.Status,
		CaseNumber:  filter.CaseNumber,
		Title:       filter.Title,
		CreatedBy:   filter.CreatedBy,
		CreatedFrom: toTimestamptz(filter.CreatedFrom),
		CreatedTo:   toTimestamptz(filter.CreatedTo),
		OffsetVal:   page.Offset(),
		LimitVal:    page.Limit(),
	}
	countArg := generated.CountCasesFilteredParams{
		Status:      filter.Status,
		CaseNumber:  filter.CaseNumber,
		Title:       filter.Title,
		CreatedBy:   filter.CreatedBy,
		CreatedFrom: listArg.CreatedFrom,
		CreatedTo:   listArg.CreatedTo,
	}

	var rows []generated.Case
	var total int64
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		repo := repository.NewCaseRepo(q)

		r, err := repo.ListFiltered(ctx, listArg)
		if err != nil {
			return fmt.Errorf("list cases: %w", err)
		}
		rows = r

		total, err = repo.CountFiltered(ctx, countArg)
		if err != nil {
			return fmt.Errorf("count cases: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, utils.ErrInternal(err)
	}

	summaries := make([]CaseSummary, len(rows))
	for i, c := range rows {
		summaries[i] = toCaseSummary(c)
	}

	return &CaseListResult{Cases: summaries, Meta: page.BuildMeta(total)}, nil
}

// ---- Get ----

// GetCase authorizes user for case:read on caseID (RBAC then ABAC — see
// authz.Service.CanAccessCase) and, only if allowed, assembles the full
// case detail: metadata, involved parties (server-side sanitized per
// authz.SanitizeInvolvedParty before ever leaving this method — master
// prompt §20), document references, timeline, and the caller's own
// relationship to the case.
//
// A case that doesn't exist and a case the caller has no relationship to
// produce the IDENTICAL error (utils.ErrForbidden, the same message
// RequireCaseAccess's middleware would already have returned) — never a
// 404 that would confirm the case's existence to an unauthorized caller
// (master prompt §14/§21).
func (s *CaseService) GetCase(ctx context.Context, user auth.AuthenticatedUser, caseID uuid.UUID) (*CaseDetail, error) {
	decision, err := s.authz.CanAccessCase(ctx, user, caseID, authz.ActionCaseRead)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !decision.Allowed {
		return nil, utils.ErrForbidden(genericCaseForbiddenMessage)
	}

	return s.loadCaseDetail(ctx, user, caseID)
}

// ---- Update ----

// UpdateCase authorizes user for case:update on caseID (RBAC then ABAC),
// validates and applies a full replacement of title/description/status/
// metadata (see UpdateCaseInput's doc comment on why this is a full
// replacement, not a partial patch), enforces the status-transition model
// documented on caseStatusTransitions, and records a CASE_UPDATED audit
// event (plus a distinct CASE_STATUS_CHANGED event when status actually
// changed) — all inside one transaction, so a validation failure or
// database error never leaves a partially-applied update or a false
// "updated" audit event (master prompt §24/§25).
func (s *CaseService) UpdateCase(ctx context.Context, user auth.AuthenticatedUser, caseID uuid.UUID, req UpdateCaseInput) (*CaseDetail, error) {
	decision, err := s.authz.CanAccessCase(ctx, user, caseID, authz.ActionCaseUpdate)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !decision.Allowed {
		return nil, utils.ErrForbidden(genericCaseForbiddenMessage)
	}

	if err := validateCaseTitle(req.Title); err != nil {
		return nil, utils.ErrBadRequest(err.Error())
	}
	if err := validateCaseDescription(req.Description); err != nil {
		return nil, utils.ErrBadRequest(err.Error())
	}
	if !caseStatuses[req.Status] {
		return nil, utils.ErrBadRequest("Invalid case status")
	}
	if err := utils.ValidateJSONMetadata(req.Metadata); err != nil {
		return nil, utils.ErrBadRequest(err.Error())
	}
	metadata := normalizeMetadata(req.Metadata)

	ident := repository.AppIdentity{UserID: user.ID, Role: effectiveCaseRole(user)}

	var before, after generated.Case
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		repo := repository.NewCaseRepo(q)

		current, err := repo.GetByID(ctx, caseID)
		if err != nil {
			return err
		}
		before = current

		if req.Status != current.Status {
			if !caseStatusTransitions[current.Status][req.Status] {
				return utils.ErrBadRequest(fmt.Sprintf("Cannot transition case status from %s to %s", current.Status, req.Status))
			}
		}

		updated, err := repo.Update(ctx, generated.UpdateCaseParams{
			ID:          caseID,
			Title:       req.Title,
			Description: req.Description,
			Status:      req.Status,
			Metadata:    metadata,
		})
		if err != nil {
			return err
		}
		after = updated
		return nil
	})
	if err != nil {
		if appErr, ok := utils.AsAppError(err); ok {
			return nil, appErr
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, utils.ErrForbidden(genericCaseForbiddenMessage)
		}
		return nil, utils.ErrInternal(err)
	}

	role := effectiveCaseRole(user)
	s.recorder.Record(ctx, audit.Event{
		Action:       "CASE_UPDATED",
		ResourceType: "case",
		ResourceID:   &after.ID,
		UserID:       &user.ID,
		Role:         role,
		CaseID:       &after.ID,
		Metadata:     map[string]any{"case_number": after.CaseNumber},
	})
	if before.Status != after.Status {
		s.recorder.Record(ctx, audit.Event{
			Action:       "CASE_STATUS_CHANGED",
			ResourceType: "case",
			ResourceID:   &after.ID,
			UserID:       &user.ID,
			Role:         role,
			CaseID:       &after.ID,
			Metadata:     map[string]any{"from": before.Status, "to": after.Status},
		})
	}

	return s.loadCaseDetail(ctx, user, caseID)
}

// ---- internal helpers ----

// loadCaseDetail assembles the full CaseDetail for caseID. Callers MUST
// have already authorized user for this case (GetCase/UpdateCase both do)
// — this helper performs no authorization check of its own; it exists
// only to avoid duplicating the assembly logic between GetCase and
// UpdateCase's response. Every query here runs under the caller's own RLS
// identity, exactly like every other case-scoped read in this file.
func (s *CaseService) loadCaseDetail(ctx context.Context, user auth.AuthenticatedUser, caseID uuid.UUID) (*CaseDetail, error) {
	ident := repository.AppIdentity{UserID: user.ID, Role: effectiveCaseRole(user)}

	var caseRow generated.Case
	var parties []generated.CaseInvolvedParty
	var documents []generated.Document
	var membership generated.CaseMember
	var hasMembership bool

	err := repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		repo := repository.NewCaseRepo(q)
		docRepo := repository.NewDocumentRepo(q)

		c, err := repo.GetByID(ctx, caseID)
		if err != nil {
			return err
		}
		caseRow = c

		p, err := repo.ListInvolvedParties(ctx, caseID)
		if err != nil {
			return fmt.Errorf("list involved parties: %w", err)
		}
		if len(p) > maxCaseDetailInvolvedParties {
			p = p[:maxCaseDetailInvolvedParties]
		}
		parties = p

		d, err := docRepo.ListByCase(ctx, caseID, maxCaseDetailDocuments, 0)
		if err != nil {
			return fmt.Errorf("list documents: %w", err)
		}
		documents = d

		m, err := repo.GetActiveMembership(ctx, caseID, user.ID)
		switch {
		case err == nil:
			membership = m
			hasMembership = true
		case errors.Is(err, pgx.ErrNoRows):
			// Not a member — fine, e.g. an ADMIN viewing a case they never
			// joined, or the not-yet-processed instant right after a
			// bootstrap membership insert in a different transaction.
		default:
			return fmt.Errorf("load membership: %w", err)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, utils.ErrForbidden(genericCaseForbiddenMessage)
		}
		return nil, utils.ErrInternal(err)
	}

	sanitizedParties := make([]InvolvedPartySummary, len(parties))
	for i, p := range parties {
		sanitized := authz.SanitizeInvolvedParty(user, p)
		sanitizedParties[i] = InvolvedPartySummary{
			ID:          sanitized.ID,
			PartyType:   sanitized.PartyType,
			DisplayName: sanitized.DisplayName,
			Metadata:    sanitized.Metadata,
			CreatedAt:   sanitized.CreatedAt,
		}
	}

	docSummaries := make([]DocumentSummary, len(documents))
	for i, d := range documents {
		docSummaries[i] = DocumentSummary{
			ID:           d.ID,
			CaseID:       d.CaseID,
			DocumentType: d.DocumentType,
			Filename:     d.Filename,
			Description:  d.Description,
			MimeType:     d.MimeType,
			FileSize:     d.FileSize,
			Sha256Hash:   hex.EncodeToString(d.Sha256Hash),
			Status:       d.Status,
			UploadedBy:   d.UploadedBy,
			UploadedAt:   d.UploadedAt,
		}
	}

	rel := CaseRelationship{IsOwner: caseRow.CreatedBy == user.ID}
	if hasMembership {
		rel.IsMember = true
		mt := membership.MembershipType
		rel.MembershipType = &mt
	}

	return &CaseDetail{
		CaseSummary:     toCaseSummary(caseRow),
		InvolvedParties: sanitizedParties,
		Documents:       docSummaries,
		Timeline:        buildTimeline(caseRow, parties, documents),
		Relationship:    rel,
	}, nil
}

// buildTimeline synthesizes a chronological timeline from already-loaded,
// case-scoped rows — see TimelineEvent's doc comment for why this reads
// no audit table.
func buildTimeline(c generated.Case, parties []generated.CaseInvolvedParty, documents []generated.Document) []TimelineEvent {
	events := make([]TimelineEvent, 0, 2+len(parties)+len(documents))

	events = append(events, TimelineEvent{
		Type:      TimelineEventCaseCreated,
		Timestamp: c.CreatedAt,
		Summary:   fmt.Sprintf("Case %s created", c.CaseNumber),
		RelatedID: &c.ID,
	})

	if c.UpdatedAt.After(c.CreatedAt) {
		events = append(events, TimelineEvent{
			Type:      TimelineEventCaseUpdated,
			Timestamp: c.UpdatedAt,
			Summary:   fmt.Sprintf("Case %s updated (status: %s)", c.CaseNumber, c.Status),
			RelatedID: &c.ID,
		})
	}

	for _, p := range parties {
		partyID := p.ID
		events = append(events, TimelineEvent{
			Type:      TimelineEventInvolvedPartyAdded,
			Timestamp: p.CreatedAt,
			Summary:   fmt.Sprintf("%s party added", p.PartyType),
			RelatedID: &partyID,
		})
	}

	for _, d := range documents {
		docID := d.ID
		events = append(events, TimelineEvent{
			Type:      TimelineEventDocumentAdded,
			Timestamp: d.UploadedAt,
			Summary:   fmt.Sprintf("Document %q added", d.Filename),
			RelatedID: &docID,
		})
	}

	sort.Slice(events, func(i, j int) bool { return events[i].Timestamp.Before(events[j].Timestamp) })
	return events
}

func toCaseSummary(c generated.Case) CaseSummary {
	return CaseSummary{
		ID:          c.ID,
		CaseNumber:  c.CaseNumber,
		Title:       c.Title,
		Description: c.Description,
		Status:      c.Status,
		Metadata:    c.Metadata,
		CreatedBy:   c.CreatedBy,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}
}

func normalizeMetadata(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}

func validateCaseNumber(v string) error {
	if v == "" {
		return errors.New("case_number is required")
	}
	if len(v) > maxCaseNumberLen {
		return fmt.Errorf("case_number must be at most %d characters", maxCaseNumberLen)
	}
	return nil
}

func validateCaseTitle(v string) error {
	if v == "" {
		return errors.New("title is required")
	}
	if len(v) > maxCaseTitleLen {
		return fmt.Errorf("title must be at most %d characters", maxCaseTitleLen)
	}
	return nil
}

func validateCaseDescription(v *string) error {
	if v == nil {
		return nil
	}
	if len(*v) > maxCaseDescLen {
		return fmt.Errorf("description must be at most %d characters", maxCaseDescLen)
	}
	return nil
}

// effectiveCaseRole picks the RLS-diagnostic "acting role" for a
// multi-role user — the exact same convention internal/authz's
// (unexported) effectiveRole uses: user.Roles[0], which is ADMIN whenever
// the user holds it (AuthenticatedUser.Roles is populated in alphabetical
// order — see AuthService.ResolveIdentity). This is duplicated here rather
// than exported from internal/authz because it is purely a display/RLS-
// role-column convention, never an authorization decision itself (every
// actual RBAC/ABAC decision still goes through authz.Service, which
// evaluates the user's FULL role set).
func effectiveCaseRole(u auth.AuthenticatedUser) string {
	if len(u.Roles) == 0 {
		return ""
	}
	return u.Roles[0]
}

func toTimestamptz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func strPtr(s string) *string { return &s }

// isUniqueViolation reports whether err is a PostgreSQL unique-violation
// (SQLSTATE 23505) on the named constraint — used to turn a duplicate
// case_number into a clean 409 rather than a raw, client-facing database
// error (master prompt §30/§34).
func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}
