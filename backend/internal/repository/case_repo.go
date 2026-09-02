package repository

import (
	"context"

	"github.com/google/uuid"

	"evidentia/backend/db/generated"
)

// CaseRepo wraps the case/case-member/involved-party queries. Every method
// runs under whatever RLS identity q's owning transaction established
// (see WithTx) — a caller with no active membership in a case simply
// never sees it, regardless of the WHERE clause in the underlying query.
type CaseRepo struct {
	q *generated.Queries
}

func NewCaseRepo(q *generated.Queries) *CaseRepo {
	return &CaseRepo{q: q}
}

func (r *CaseRepo) Create(ctx context.Context, arg generated.CreateCaseParams) (generated.Case, error) {
	return r.q.CreateCase(ctx, arg)
}

func (r *CaseRepo) GetByID(ctx context.Context, id uuid.UUID) (generated.Case, error) {
	return r.q.GetCaseByID(ctx, id)
}

func (r *CaseRepo) GetByCaseNumber(ctx context.Context, caseNumber string) (generated.Case, error) {
	return r.q.GetCaseByCaseNumber(ctx, caseNumber)
}

func (r *CaseRepo) List(ctx context.Context, limit, offset int32) ([]generated.Case, error) {
	return r.q.ListCases(ctx, generated.ListCasesParams{Limit: limit, Offset: offset})
}

func (r *CaseRepo) ListByStatus(ctx context.Context, status string, limit, offset int32) ([]generated.Case, error) {
	return r.q.ListCasesByStatus(ctx, generated.ListCasesByStatusParams{Status: status, Limit: limit, Offset: offset})
}

func (r *CaseRepo) Count(ctx context.Context) (int64, error) {
	return r.q.CountCases(ctx)
}

func (r *CaseRepo) Update(ctx context.Context, arg generated.UpdateCaseParams) (generated.Case, error) {
	return r.q.UpdateCase(ctx, arg)
}

// ---- Case members ----

func (r *CaseRepo) AddMember(ctx context.Context, arg generated.AddCaseMemberParams) (generated.CaseMember, error) {
	return r.q.AddCaseMember(ctx, arg)
}

func (r *CaseRepo) RemoveMember(ctx context.Context, caseID, userID uuid.UUID) error {
	return r.q.RemoveCaseMember(ctx, generated.RemoveCaseMemberParams{CaseID: caseID, UserID: userID})
}

func (r *CaseRepo) GetActiveMembership(ctx context.Context, caseID, userID uuid.UUID) (generated.CaseMember, error) {
	return r.q.GetActiveCaseMembership(ctx, generated.GetActiveCaseMembershipParams{CaseID: caseID, UserID: userID})
}

func (r *CaseRepo) ListMembers(ctx context.Context, caseID uuid.UUID) ([]generated.CaseMember, error) {
	return r.q.ListCaseMembers(ctx, caseID)
}

func (r *CaseRepo) ListActiveCasesForUser(ctx context.Context, userID uuid.UUID) ([]generated.ListActiveCasesForUserRow, error) {
	return r.q.ListActiveCasesForUser(ctx, userID)
}

// ---- Involved parties ----

func (r *CaseRepo) CreateInvolvedParty(ctx context.Context, arg generated.CreateInvolvedPartyParams) (generated.CaseInvolvedParty, error) {
	return r.q.CreateInvolvedParty(ctx, arg)
}

func (r *CaseRepo) GetInvolvedPartyByID(ctx context.Context, id uuid.UUID) (generated.CaseInvolvedParty, error) {
	return r.q.GetInvolvedPartyByID(ctx, id)
}

func (r *CaseRepo) ListInvolvedParties(ctx context.Context, caseID uuid.UUID) ([]generated.CaseInvolvedParty, error) {
	return r.q.ListInvolvedPartiesByCase(ctx, caseID)
}
