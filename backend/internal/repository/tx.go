// Package repository is the data-access layer: thin wrappers around
// sqlc-generated queries (db/generated), plus the transaction foundation
// (WithTx) that establishes the transaction-local RLS identity every
// protected-table query depends on. It intentionally does not implement
// business workflows — see the individual repo files (UserRepo, CaseRepo,
// ...), which stay close to 1:1 with the underlying sqlc queries.
package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"evidentia/backend/db/generated"
)

// AppIdentity is the transaction-local RLS context every request-scoped
// transaction must establish before touching an RLS-protected table (see
// current_app_user_id()/current_app_role() in
// db/migrations/000001_init_schema.up.sql). The zero value (UserID ==
// uuid.Nil, Role == "") means "no identity" — RLS then fails closed, which
// is the correct behavior for an unauthenticated or background-job
// context, not a bug to work around.
type AppIdentity struct {
	UserID uuid.UUID
	Role   string
}

// WithTx runs fn inside a single database transaction with ident applied
// via set_config(..., true) (the "true" third argument scopes both
// settings to this transaction only, per master prompt §34 — they can
// never leak to another request reusing the same pooled connection
// afterward, committed or not). fn receives a *generated.Queries bound to
// the transaction; a non-nil error rolls back, otherwise the transaction
// commits. This is the ONLY correct way to run a query against an
// RLS-protected table — even a single read needs the identity context
// established first, so there is deliberately no non-transactional query
// path in this package.
func WithTx(ctx context.Context, pool *pgxpool.Pool, ident AppIdentity, fn func(ctx context.Context, q *generated.Queries) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository: begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once committed

	userID := ""
	if ident.UserID != uuid.Nil {
		userID = ident.UserID.String()
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, userID); err != nil {
		return fmt.Errorf("repository: set app.user_id: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.role', $1, true)`, ident.Role); err != nil {
		return fmt.Errorf("repository: set app.role: %w", err)
	}

	if err := fn(ctx, generated.New(tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository: commit transaction: %w", err)
	}
	return nil
}
