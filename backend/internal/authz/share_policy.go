package authz

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"evidentia/backend/db/generated"
	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/models"
	"evidentia/backend/internal/repository"
)

// shareViewActions is every Action a VIEW-tier share entitles its
// recipient to — master prompt §6/§26: "VIEW" covers reading and
// downloading the document (this application has no distinct
// metadata-only view separate from download — see
// db/migrations/000004_document_sharing.up.sql's table comment) plus
// reading its compliance certificate (no more sensitive than the hash it
// already contains). Deliberately absent: ActionDocumentRedact,
// ActionDocumentShare, ActionCertificateCreate, ActionDocumentUpload, and
// every non-document action — a share can NEVER cover these regardless of
// permission tier (master prompt §7/§25's "VIEW does not imply
// REDACT/SHARE/DELETE"), enforced here structurally (they are simply
// never present in either map below) rather than by a runtime check that
// could be gotten wrong.
var shareViewActions = map[Action]bool{
	ActionDocumentRead:     true,
	ActionDocumentDownload: true,
	ActionCertificateRead:  true,
}

// shareVerifyActions is what a VERIFY-tier share ADDS on top of
// shareViewActions (master prompt §24: VERIFY is an explicit add-on, not
// implied by VIEW).
var shareVerifyActions = map[Action]bool{
	ActionDocumentVerify: true,
}

// sharePermissionCovers reports whether a share carrying permission
// entitles its recipient to perform action. Called only for actions that
// appear in shareViewActions/shareVerifyActions to begin with — see
// shareGrantsAccess, which short-circuits to false for every other action
// before ever consulting the database.
func sharePermissionCovers(permission string, action Action) bool {
	if shareViewActions[action] {
		return true
	}
	return permission == models.SharePermissionVerify && shareVerifyActions[action]
}

// shareCoverableAction reports whether action is ever something a
// document share COULD grant, for any permission tier — the hard
// architectural boundary from master prompt §7/§25: redaction,
// resharing, certificate creation, and upload are never reachable through
// delegated access no matter what a document_shares row says, so
// shareGrantsAccess never even queries the database for them.
func shareCoverableAction(action Action) bool {
	return shareViewActions[action] || shareVerifyActions[action]
}

// shareGrantsAccess reports whether user currently holds an ACTIVE,
// unexpired document_shares grant for documentID that entitles them to
// action — the application-layer half of master prompt §19's "user is
// directly authorized OR user has active valid delegated access" (the
// database-level half is documents_select/compliance_certificates_select's
// own OR-branch — see the migration). Queried under the caller's own RLS
// identity (repository.WithTx with their real user ID), never a
// privileged bypass, so a share row this RLS policy would itself deny is
// denied here too, not just trusted because the Go code found it.
func (s *Service) shareGrantsAccess(ctx context.Context, user auth.AuthenticatedUser, documentID uuid.UUID, action Action) (bool, error) {
	if !shareCoverableAction(action) {
		return false, nil
	}

	ident := repository.AppIdentity{UserID: user.ID, Role: effectiveRole(user)}
	var share generated.DocumentShare
	var found bool
	err := repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		sh, err := repository.NewShareRepo(q).GetActiveForDocumentAndUser(ctx, documentID, user.ID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		share = sh
		found = true
		return nil
	})
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}

	// Belt-and-suspenders: the query itself already filters on
	// expires_at, but re-checking here means this function's contract
	// never silently depends on that SQL clause alone.
	if share.ExpiresAt.Valid && !share.ExpiresAt.Time.After(time.Now()) {
		return false, nil
	}

	return sharePermissionCovers(share.Permission, action), nil
}
