//go:build integration

// Run with: go test -tags=integration ./internal/service/...
// Requires the docker-compose postgres AND minio services up, migrated
// (including 000004_document_sharing), seeded. See
// document_service_integration_test.go/document_redact_integration_test.go
// for the shared helpers (newDocumentServiceForTest, mustSeedCase,
// mustAddCaseMember, authUser, newUserWithRole, spyRecorder,
// utilsAsAppError) this file reuses.
package service

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/audit"
	"evidentia/backend/internal/authz"
	"evidentia/backend/internal/events"
	"evidentia/backend/internal/models"
)

// newShareServiceForTest wires a real ShareService against the live
// docker-compose PostgreSQL.
func newShareServiceForTest(t *testing.T, recorder audit.Recorder) *ShareService {
	t.Helper()
	appDB := appPool(t)
	authzService := authz.NewService(appDB, recorder)
	return NewShareService(appDB, authzService, recorder, events.NoopPublisher{})
}

// mustSeedDocument inserts a document row directly (bypassing
// DocumentService/object storage entirely, the same convention
// mustSeedCase uses for cases) — valid here because ShareService never
// touches document bytes. Tests that need real storage/hashing (the
// integrity test below) go through DocumentService.UploadDocument
// instead.
func mustSeedDocument(t *testing.T, pool *pgxpool.Pool, caseID, uploadedBy uuid.UUID, filename string) uuid.UUID {
	t.Helper()
	hash := make([]byte, 32)
	for i := range hash {
		hash[i] = byte(i)
	}
	key := "cases/" + caseID.String() + "/documents/" + uuid.New().String() + "/original"

	var docID uuid.UUID
	require.NoError(t, pool.QueryRow(context.Background(), `
		INSERT INTO documents (case_id, document_type, filename, mime_type, file_size, sha256_hash, storage_bucket, storage_object_key, uploaded_by)
		VALUES ($1, 'OTHER', $2, 'text/plain', 5, $3, 'test-bucket', $4, $5)
		RETURNING id`,
		caseID, filename, hash, key, uploadedBy,
	).Scan(&docID))
	return docID
}

func mustDeactivateUser(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `UPDATE users SET status = 'inactive' WHERE id = $1`, userID)
	require.NoError(t, err)
}

// ---- Create ----

func TestShareService_CreateShare_AuthorizedPoliceCanShare(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	rec := &spyRecorder{}
	svc := newShareServiceForTest(t, rec)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "share-officer1@example.com", models.RolePolice)
	lawyer := newUserWithRole(t, migrator, "share-lawyer1@example.com", models.RoleLawyer)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-1", officer)
	docID := mustSeedDocument(t, migrator, caseID, officer, "evidence.txt")

	summary, err := svc.CreateShare(ctx, authUser(officer, models.RolePolice), docID, CreateShareInput{
		RecipientUserID: lawyer,
		Permission:      models.SharePermissionView,
	})
	require.NoError(t, err)
	assert.Equal(t, docID, summary.DocumentID)
	assert.Equal(t, lawyer, summary.RecipientUserID)
	assert.Equal(t, officer, summary.CreatedByUserID)
	assert.Equal(t, models.SharePermissionView, summary.Permission)
	assert.Equal(t, models.ShareStatusActive, summary.Status)
	assert.Equal(t, models.ShareEffectiveStatusActive, summary.EffectiveStatus)
	assert.Nil(t, summary.ExpiresAt)
	assert.Contains(t, rec.actions(), "DOCUMENT_SHARED")
}

func TestShareService_CreateShare_ForensicsCannotShare(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	rec := &spyRecorder{}
	svc := newShareServiceForTest(t, rec)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "share-officer2@example.com", models.RolePolice)
	forensics := newUserWithRole(t, migrator, "share-forensics2@example.com", models.RoleForensics)
	lawyer := newUserWithRole(t, migrator, "share-lawyer2@example.com", models.RoleLawyer)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-2", officer)
	mustAddCaseMember(t, migrator, caseID, forensics, officer, models.MembershipTypeForensics)
	docID := mustSeedDocument(t, migrator, caseID, officer, "evidence.txt")

	_, err := svc.CreateShare(ctx, authUser(forensics, models.RoleForensics), docID, CreateShareInput{
		RecipientUserID: lawyer,
		Permission:      models.SharePermissionView,
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status, "FORENSICS holds no document:share permission")
	assert.NotContains(t, rec.actions(), "DOCUMENT_SHARED")
}

func TestShareService_CreateShare_UnrelatedCaseDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc := newShareServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officerA := newUserWithRole(t, migrator, "share-officerA3@example.com", models.RolePolice)
	officerB := newUserWithRole(t, migrator, "share-officerB3@example.com", models.RolePolice)
	lawyer := newUserWithRole(t, migrator, "share-lawyer3@example.com", models.RoleLawyer)
	caseA := mustSeedCase(t, migrator, "SHARE-CASE-3", officerA)
	docA := mustSeedDocument(t, migrator, caseA, officerA, "evidence.txt")

	// officerB holds document:share (POLICE) but has no relationship to
	// caseA — an IDOR attempt against a document they merely guessed the
	// ID of.
	_, err := svc.CreateShare(ctx, authUser(officerB, models.RolePolice), docA, CreateShareInput{
		RecipientUserID: lawyer,
		Permission:      models.SharePermissionView,
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status)
}

func TestShareService_CreateShare_SelfShareRejected(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc := newShareServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "share-officer4@example.com", models.RolePolice)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-4", officer)
	docID := mustSeedDocument(t, migrator, caseID, officer, "evidence.txt")

	_, err := svc.CreateShare(ctx, authUser(officer, models.RolePolice), docID, CreateShareInput{
		RecipientUserID: officer,
		Permission:      models.SharePermissionView,
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 400, appErr.Status)
}

func TestShareService_CreateShare_InvalidPermissionRejected(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc := newShareServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "share-officer5@example.com", models.RolePolice)
	lawyer := newUserWithRole(t, migrator, "share-lawyer5@example.com", models.RoleLawyer)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-5", officer)
	docID := mustSeedDocument(t, migrator, caseID, officer, "evidence.txt")

	for _, perm := range []string{"EDIT", "DELETE", "REDACT", "RESHARE", "OWNER", ""} {
		_, err := svc.CreateShare(ctx, authUser(officer, models.RolePolice), docID, CreateShareInput{
			RecipientUserID: lawyer,
			Permission:      perm,
		})
		require.Error(t, err, "permission %q must be rejected", perm)
		appErr, ok := utilsAsAppError(err)
		require.True(t, ok)
		assert.Equal(t, 400, appErr.Status)
	}
}

func TestShareService_CreateShare_ExpirationInPastRejected(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc := newShareServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "share-officer6@example.com", models.RolePolice)
	lawyer := newUserWithRole(t, migrator, "share-lawyer6@example.com", models.RoleLawyer)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-6", officer)
	docID := mustSeedDocument(t, migrator, caseID, officer, "evidence.txt")

	past := time.Now().Add(-time.Hour)
	_, err := svc.CreateShare(ctx, authUser(officer, models.RolePolice), docID, CreateShareInput{
		RecipientUserID: lawyer,
		Permission:      models.SharePermissionView,
		ExpiresAt:       &past,
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 400, appErr.Status)
}

func TestShareService_CreateShare_InactiveRecipientRejected(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc := newShareServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "share-officer7@example.com", models.RolePolice)
	lawyer := newUserWithRole(t, migrator, "share-lawyer7@example.com", models.RoleLawyer)
	mustDeactivateUser(t, migrator, lawyer)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-7", officer)
	docID := mustSeedDocument(t, migrator, caseID, officer, "evidence.txt")

	_, err := svc.CreateShare(ctx, authUser(officer, models.RolePolice), docID, CreateShareInput{
		RecipientUserID: lawyer,
		Permission:      models.SharePermissionView,
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 400, appErr.Status)
}

func TestShareService_CreateShare_NonexistentRecipientRejected(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc := newShareServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "share-officer8@example.com", models.RolePolice)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-8", officer)
	docID := mustSeedDocument(t, migrator, caseID, officer, "evidence.txt")

	_, err := svc.CreateShare(ctx, authUser(officer, models.RolePolice), docID, CreateShareInput{
		RecipientUserID: uuid.New(),
		Permission:      models.SharePermissionView,
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 400, appErr.Status)
}

func TestShareService_CreateShare_DuplicateActiveShareConflict(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc := newShareServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "share-officer9@example.com", models.RolePolice)
	lawyer := newUserWithRole(t, migrator, "share-lawyer9@example.com", models.RoleLawyer)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-9", officer)
	docID := mustSeedDocument(t, migrator, caseID, officer, "evidence.txt")

	_, err := svc.CreateShare(ctx, authUser(officer, models.RolePolice), docID, CreateShareInput{
		RecipientUserID: lawyer,
		Permission:      models.SharePermissionView,
	})
	require.NoError(t, err)

	_, err = svc.CreateShare(ctx, authUser(officer, models.RolePolice), docID, CreateShareInput{
		RecipientUserID: lawyer,
		Permission:      models.SharePermissionVerify,
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 409, appErr.Status)
}

func TestShareService_CreateShare_AdminAllowed(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc := newShareServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "share-officer10@example.com", models.RolePolice)
	admin := newUserWithRole(t, migrator, "share-admin10@example.com", models.RoleAdmin)
	lawyer := newUserWithRole(t, migrator, "share-lawyer10@example.com", models.RoleLawyer)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-10", officer)
	docID := mustSeedDocument(t, migrator, caseID, officer, "evidence.txt")

	_, err := svc.CreateShare(ctx, authUser(admin, models.RoleAdmin), docID, CreateShareInput{
		RecipientUserID: lawyer,
		Permission:      models.SharePermissionView,
	})
	require.NoError(t, err, "ADMIN can share any document per policy")
}

// ---- List ----

func TestShareService_ListShares_CreatorSeesAll(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc := newShareServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "share-officer11@example.com", models.RolePolice)
	lawyer := newUserWithRole(t, migrator, "share-lawyer11@example.com", models.RoleLawyer)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-11", officer)
	docID := mustSeedDocument(t, migrator, caseID, officer, "evidence.txt")

	created, err := svc.CreateShare(ctx, authUser(officer, models.RolePolice), docID, CreateShareInput{
		RecipientUserID: lawyer,
		Permission:      models.SharePermissionView,
	})
	require.NoError(t, err)

	shares, err := svc.ListShares(ctx, authUser(officer, models.RolePolice), docID)
	require.NoError(t, err)
	require.Len(t, shares, 1)
	assert.Equal(t, created.ShareID, shares[0].ShareID)
}

func TestShareService_ListShares_RecipientAloneCannotList(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc := newShareServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "share-officer12@example.com", models.RolePolice)
	lawyer := newUserWithRole(t, migrator, "share-lawyer12@example.com", models.RoleLawyer)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-12", officer)
	docID := mustSeedDocument(t, migrator, caseID, officer, "evidence.txt")

	_, err := svc.CreateShare(ctx, authUser(officer, models.RolePolice), docID, CreateShareInput{
		RecipientUserID: lawyer,
		Permission:      models.SharePermissionView,
	})
	require.NoError(t, err)

	// The recipient holds document:share (LAWYER's own RBAC grant) but is
	// not a member of the document's case and the share itself can never
	// cover ActionDocumentShare — must not be able to see who ELSE the
	// document is shared with.
	_, err = svc.ListShares(ctx, authUser(lawyer, models.RoleLawyer), docID)
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status)
}

// ---- Revoke ----

func TestShareService_RevokeShare_Success(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	rec := &spyRecorder{}
	svc := newShareServiceForTest(t, rec)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "share-officer13@example.com", models.RolePolice)
	lawyer := newUserWithRole(t, migrator, "share-lawyer13@example.com", models.RoleLawyer)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-13", officer)
	docID := mustSeedDocument(t, migrator, caseID, officer, "evidence.txt")

	created, err := svc.CreateShare(ctx, authUser(officer, models.RolePolice), docID, CreateShareInput{
		RecipientUserID: lawyer,
		Permission:      models.SharePermissionView,
	})
	require.NoError(t, err)

	rec.events = nil
	revoked, err := svc.RevokeShare(ctx, authUser(officer, models.RolePolice), docID, created.ShareID)
	require.NoError(t, err)
	assert.Equal(t, models.ShareStatusRevoked, revoked.Status)
	assert.Equal(t, models.ShareEffectiveStatusRevoked, revoked.EffectiveStatus)
	assert.NotNil(t, revoked.RevokedAt)
	require.NotNil(t, revoked.RevokedByUserID)
	assert.Equal(t, officer, *revoked.RevokedByUserID)
	assert.Contains(t, rec.actions(), "DOCUMENT_SHARE_REVOKED")
}

func TestShareService_RevokeShare_AlreadyRevokedNotFound(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc := newShareServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "share-officer14@example.com", models.RolePolice)
	lawyer := newUserWithRole(t, migrator, "share-lawyer14@example.com", models.RoleLawyer)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-14", officer)
	docID := mustSeedDocument(t, migrator, caseID, officer, "evidence.txt")

	created, err := svc.CreateShare(ctx, authUser(officer, models.RolePolice), docID, CreateShareInput{
		RecipientUserID: lawyer,
		Permission:      models.SharePermissionView,
	})
	require.NoError(t, err)

	_, err = svc.RevokeShare(ctx, authUser(officer, models.RolePolice), docID, created.ShareID)
	require.NoError(t, err)

	_, err = svc.RevokeShare(ctx, authUser(officer, models.RolePolice), docID, created.ShareID)
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 404, appErr.Status)
}

func TestShareService_RevokeShare_CrossDocumentShareIDDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc := newShareServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "share-officer15@example.com", models.RolePolice)
	lawyer := newUserWithRole(t, migrator, "share-lawyer15@example.com", models.RoleLawyer)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-15", officer)
	docA := mustSeedDocument(t, migrator, caseID, officer, "a.txt")
	docB := mustSeedDocument(t, migrator, caseID, officer, "b.txt")

	created, err := svc.CreateShare(ctx, authUser(officer, models.RolePolice), docA, CreateShareInput{
		RecipientUserID: lawyer,
		Permission:      models.SharePermissionView,
	})
	require.NoError(t, err)

	// shareId is real, but belongs to docA — attempting to revoke it
	// through docB's URL must fail identically to a nonexistent share ID.
	_, err = svc.RevokeShare(ctx, authUser(officer, models.RolePolice), docB, created.ShareID)
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 404, appErr.Status)
}

func TestShareService_RevokeShare_UnauthorizedDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc := newShareServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "share-officer16@example.com", models.RolePolice)
	forensics := newUserWithRole(t, migrator, "share-forensics16@example.com", models.RoleForensics)
	lawyer := newUserWithRole(t, migrator, "share-lawyer16@example.com", models.RoleLawyer)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-16", officer)
	mustAddCaseMember(t, migrator, caseID, forensics, officer, models.MembershipTypeForensics)
	docID := mustSeedDocument(t, migrator, caseID, officer, "evidence.txt")

	created, err := svc.CreateShare(ctx, authUser(officer, models.RolePolice), docID, CreateShareInput{
		RecipientUserID: lawyer,
		Permission:      models.SharePermissionView,
	})
	require.NoError(t, err)

	_, err = svc.RevokeShare(ctx, authUser(forensics, models.RoleForensics), docID, created.ShareID)
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status, "FORENSICS holds no document:share permission, even as a genuine case member")
}

// ---- Delegated access (the real point of this system) ----

func TestShareService_DelegatedAccess_ViewGrantsDownloadNotVerify(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	shareSvc := newShareServiceForTest(t, &spyRecorder{})
	docSvc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "share-officer17@example.com", models.RolePolice)
	lawyer := newUserWithRole(t, migrator, "share-lawyer17@example.com", models.RoleLawyer)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-17", officer)

	content := []byte("shared evidence content")
	uploaded, err := docSvc.UploadDocument(ctx, authUser(officer, models.RolePolice), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeOther,
		Filename:     "shared.txt",
		File:         bytes.NewReader(content),
	})
	require.NoError(t, err)

	_, err = shareSvc.CreateShare(ctx, authUser(officer, models.RolePolice), uploaded.ID, CreateShareInput{
		RecipientUserID: lawyer,
		Permission:      models.SharePermissionView,
	})
	require.NoError(t, err)

	// Lawyer is NOT a member of this case — the ONLY reason this
	// succeeds is the active VIEW share.
	downloaded, err := docSvc.DownloadDocument(ctx, authUser(lawyer, models.RoleLawyer), uploaded.ID)
	require.NoError(t, err, "an active VIEW share must grant download")
	downloaded.Content.Close()

	_, err = docSvc.VerifyDocument(ctx, authUser(lawyer, models.RoleLawyer), uploaded.ID)
	require.Error(t, err, "VIEW must NOT imply VERIFY")
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status)
}

func TestShareService_DelegatedAccess_VerifyGrantsBoth(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	shareSvc := newShareServiceForTest(t, &spyRecorder{})
	docSvc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officerA := newUserWithRole(t, migrator, "share-officerA18@example.com", models.RolePolice)
	// FORENSICS holds document:verify via RBAC (unlike LAWYER/JUDGE, per
	// the seed data) — the relevant recipient for this test: sharing can
	// only ever bypass the CASE-relationship requirement, never grant an
	// action-type a recipient's ROLE doesn't hold at all (RBAC is
	// checked first in CanAccessDocument, before any share is even
	// consulted — see internal/authz/document_policy.go).
	forensics := newUserWithRole(t, migrator, "share-forensics18@example.com", models.RoleForensics)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-18", officerA)

	uploaded, err := docSvc.UploadDocument(ctx, authUser(officerA, models.RolePolice), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeOther,
		Filename:     "shared.txt",
		File:         bytes.NewReader([]byte("content")),
	})
	require.NoError(t, err)

	_, err = shareSvc.CreateShare(ctx, authUser(officerA, models.RolePolice), uploaded.ID, CreateShareInput{
		RecipientUserID: forensics,
		Permission:      models.SharePermissionVerify,
	})
	require.NoError(t, err)

	downloaded, err := docSvc.DownloadDocument(ctx, authUser(forensics, models.RoleForensics), uploaded.ID)
	require.NoError(t, err)
	downloaded.Content.Close()

	result, err := docSvc.VerifyDocument(ctx, authUser(forensics, models.RoleForensics), uploaded.ID)
	require.NoError(t, err)
	assert.Equal(t, VerificationStatusVerified, result.Status)
}

func TestShareService_DelegatedAccess_RevokedShareDeniesAccess(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	shareSvc := newShareServiceForTest(t, &spyRecorder{})
	docSvc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "share-officer19@example.com", models.RolePolice)
	lawyer := newUserWithRole(t, migrator, "share-lawyer19@example.com", models.RoleLawyer)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-19", officer)

	uploaded, err := docSvc.UploadDocument(ctx, authUser(officer, models.RolePolice), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeOther,
		Filename:     "shared.txt",
		File:         bytes.NewReader([]byte("content")),
	})
	require.NoError(t, err)

	created, err := shareSvc.CreateShare(ctx, authUser(officer, models.RolePolice), uploaded.ID, CreateShareInput{
		RecipientUserID: lawyer,
		Permission:      models.SharePermissionView,
	})
	require.NoError(t, err)

	downloaded, err := docSvc.DownloadDocument(ctx, authUser(lawyer, models.RoleLawyer), uploaded.ID)
	require.NoError(t, err, "access must work before revocation")
	downloaded.Content.Close()

	_, err = shareSvc.RevokeShare(ctx, authUser(officer, models.RolePolice), uploaded.ID, created.ShareID)
	require.NoError(t, err)

	_, err = docSvc.DownloadDocument(ctx, authUser(lawyer, models.RoleLawyer), uploaded.ID)
	require.Error(t, err, "access must be denied immediately after revocation")
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status)
}

func TestShareService_DelegatedAccess_ExpiredShareDeniesAccess(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	shareSvc := newShareServiceForTest(t, &spyRecorder{})
	docSvc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "share-officer20@example.com", models.RolePolice)
	lawyer := newUserWithRole(t, migrator, "share-lawyer20@example.com", models.RoleLawyer)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-20", officer)

	uploaded, err := docSvc.UploadDocument(ctx, authUser(officer, models.RolePolice), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeOther,
		Filename:     "shared.txt",
		File:         bytes.NewReader([]byte("content")),
	})
	require.NoError(t, err)

	expiresAt := time.Now().Add(1200 * time.Millisecond)
	_, err = shareSvc.CreateShare(ctx, authUser(officer, models.RolePolice), uploaded.ID, CreateShareInput{
		RecipientUserID: lawyer,
		Permission:      models.SharePermissionView,
		ExpiresAt:       &expiresAt,
	})
	require.NoError(t, err)

	downloaded, err := docSvc.DownloadDocument(ctx, authUser(lawyer, models.RoleLawyer), uploaded.ID)
	require.NoError(t, err, "access must work before expiration")
	downloaded.Content.Close()

	time.Sleep(1500 * time.Millisecond)

	_, err = docSvc.DownloadDocument(ctx, authUser(lawyer, models.RoleLawyer), uploaded.ID)
	require.Error(t, err, "access must be denied server-side once expires_at has passed")
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status)
}

// ---- Privilege escalation ----

func TestShareService_DelegatedAccess_CannotRedactViaShare(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	shareSvc := newShareServiceForTest(t, &spyRecorder{})
	docSvc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	admin := newUserWithRole(t, migrator, "share-admin21@example.com", models.RoleAdmin)
	lawyer := newUserWithRole(t, migrator, "share-lawyer21@example.com", models.RoleLawyer)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-21", admin)

	uploaded, err := docSvc.UploadDocument(ctx, authUser(admin, models.RoleAdmin), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeOther,
		Filename:     "shared.png",
		File:         bytes.NewReader([]byte("content")),
	})
	require.NoError(t, err)

	_, err = shareSvc.CreateShare(ctx, authUser(admin, models.RoleAdmin), uploaded.ID, CreateShareInput{
		RecipientUserID: lawyer,
		Permission:      models.SharePermissionVerify, // the HIGHEST tier — still must not grant redact
	})
	require.NoError(t, err)

	_, err = docSvc.RedactDocument(ctx, authUser(lawyer, models.RoleLawyer), uploaded.ID, RedactDocumentInput{
		Reason:  "Attempted redaction via delegated access",
		Regions: []RedactRegion{{Page: 1, X: 0, Y: 0, Width: 1, Height: 1}},
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status, "a share must never grant document:redact, at any permission tier")
}

func TestShareService_DelegatedAccess_CannotReshareViaShare(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	shareSvc := newShareServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "share-officer22@example.com", models.RolePolice)
	lawyer := newUserWithRole(t, migrator, "share-lawyer22@example.com", models.RoleLawyer)
	thirdParty := newUserWithRole(t, migrator, "share-third22@example.com", models.RoleJudge)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-22", officer)
	docID := mustSeedDocument(t, migrator, caseID, officer, "evidence.txt")

	_, err := shareSvc.CreateShare(ctx, authUser(officer, models.RolePolice), docID, CreateShareInput{
		RecipientUserID: lawyer,
		Permission:      models.SharePermissionVerify,
	})
	require.NoError(t, err)

	_, err = shareSvc.CreateShare(ctx, authUser(lawyer, models.RoleLawyer), docID, CreateShareInput{
		RecipientUserID: thirdParty,
		Permission:      models.SharePermissionView,
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status, "a share recipient must not be able to reshare the document")
}

func TestShareService_DelegatedAccess_CertificateFollowsViewPermission(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	shareSvc := newShareServiceForTest(t, &spyRecorder{})
	docSvc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	certSvc := newCertificateServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "share-officer23@example.com", models.RolePolice)
	admin := newUserWithRole(t, migrator, "share-admin23@example.com", models.RoleAdmin)
	// JUDGE holds certificate:read via RBAC (unlike LAWYER/POLICE/
	// FORENSICS) — the relevant recipient for this test, per the seed
	// data's actual permission matrix.
	judge := newUserWithRole(t, migrator, "share-judge23@example.com", models.RoleJudge)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-23", officer)

	uploaded, err := docSvc.UploadDocument(ctx, authUser(officer, models.RolePolice), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeOther,
		Filename:     "shared.txt",
		File:         bytes.NewReader([]byte("content")),
	})
	require.NoError(t, err)

	// certificate:create is ADMIN-only per the seed data — generate the
	// certificate this test's VIEW-share recipient will then read.
	_, err = certSvc.GetOrCreateCertificate(ctx, authUser(admin, models.RoleAdmin), uploaded.ID)
	require.NoError(t, err)

	_, err = shareSvc.CreateShare(ctx, authUser(officer, models.RolePolice), uploaded.ID, CreateShareInput{
		RecipientUserID: judge,
		Permission:      models.SharePermissionView,
	})
	require.NoError(t, err)

	// The judge is not a case member — certificate access must still
	// work purely via the VIEW share (master prompt §26: certificate
	// access follows document view permission).
	_, err = certSvc.GetOrCreateCertificate(ctx, authUser(judge, models.RoleJudge), uploaded.ID)
	require.NoError(t, err)
}

// ---- Redacted derivative sharing (master prompt §27/§28/§52) ----

func TestShareService_RedactedDerivative_SharingDerivativeDoesNotGrantOriginal(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	shareSvc := newShareServiceForTest(t, &spyRecorder{})
	docSvc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "share-officer24@example.com", models.RolePolice)
	admin := newUserWithRole(t, migrator, "share-admin24@example.com", models.RoleAdmin)
	lawyer := newUserWithRole(t, migrator, "share-lawyer24@example.com", models.RoleLawyer)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-24", officer)

	pngBytes, _ := redactTestPNG(t)
	original, err := docSvc.UploadDocument(ctx, authUser(officer, models.RolePolice), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeWitnessStatement,
		Filename:     "witness.png",
		File:         bytes.NewReader(pngBytes),
	})
	require.NoError(t, err)

	// document:redact is ADMIN-only per existing policy — see
	// document_redact_integration_test.go's package doc comment.
	redaction, err := docSvc.RedactDocument(ctx, authUser(admin, models.RoleAdmin), original.ID, RedactDocumentInput{
		Reason:  "Protect witness identity",
		Regions: []RedactRegion{{Page: 1, X: 0, Y: 0, Width: 5, Height: 5}},
	})
	require.NoError(t, err)
	derivativeID := redaction.Document.ID

	// Share ONLY the derivative.
	_, err = shareSvc.CreateShare(ctx, authUser(officer, models.RolePolice), derivativeID, CreateShareInput{
		RecipientUserID: lawyer,
		Permission:      models.SharePermissionView,
	})
	require.NoError(t, err)

	derivDownload, err := docSvc.DownloadDocument(ctx, authUser(lawyer, models.RoleLawyer), derivativeID)
	require.NoError(t, err, "the lawyer CAN access the shared derivative")
	derivDownload.Content.Close()

	_, err = docSvc.DownloadDocument(ctx, authUser(lawyer, models.RoleLawyer), original.ID)
	require.Error(t, err, "the lawyer must NOT automatically gain access to the original merely because the derivative was shared")
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status)
}

// ---- Document integrity (master prompt §51) ----

func TestShareService_DocumentIntegrity_SharingDoesNotChangeHash(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	shareSvc := newShareServiceForTest(t, &spyRecorder{})
	docSvc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "share-officer25@example.com", models.RolePolice)
	// FORENSICS holds document:verify via RBAC (see the
	// VerifyGrantsBoth test above for why LAWYER/JUDGE cannot be used
	// here).
	forensics := newUserWithRole(t, migrator, "share-forensics25@example.com", models.RoleForensics)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-25", officer)

	content := []byte("evidence content that must never change")
	uploaded, err := docSvc.UploadDocument(ctx, authUser(officer, models.RolePolice), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeOther,
		Filename:     "evidence.txt",
		File:         bytes.NewReader(content),
	})
	require.NoError(t, err)
	h1 := uploaded.Sha256Hash

	_, err = shareSvc.CreateShare(ctx, authUser(officer, models.RolePolice), uploaded.ID, CreateShareInput{
		RecipientUserID: forensics,
		Permission:      models.SharePermissionVerify,
	})
	require.NoError(t, err)

	downloaded, err := docSvc.DownloadDocument(ctx, authUser(forensics, models.RoleForensics), uploaded.ID)
	require.NoError(t, err)
	gotBytes, err := io.ReadAll(downloaded.Content)
	downloaded.Content.Close()
	require.NoError(t, err)
	assert.Equal(t, content, gotBytes)

	result, err := docSvc.VerifyDocument(ctx, authUser(forensics, models.RoleForensics), uploaded.ID)
	require.NoError(t, err)
	assert.Equal(t, VerificationStatusVerified, result.Status)
	assert.Equal(t, h1, result.StoredHash, "sharing must never change the document's canonical hash")
}

// ---- Shared With Me / recipient search ----

func TestShareService_ListSharedWithMe(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc := newShareServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "share-officer26@example.com", models.RolePolice)
	lawyer := newUserWithRole(t, migrator, "share-lawyer26@example.com", models.RoleLawyer)
	caseID := mustSeedCase(t, migrator, "SHARE-CASE-26", officer)
	docA := mustSeedDocument(t, migrator, caseID, officer, "a.txt")
	docB := mustSeedDocument(t, migrator, caseID, officer, "b.txt")

	shareA, err := svc.CreateShare(ctx, authUser(officer, models.RolePolice), docA, CreateShareInput{
		RecipientUserID: lawyer, Permission: models.SharePermissionView,
	})
	require.NoError(t, err)
	_, err = svc.CreateShare(ctx, authUser(officer, models.RolePolice), docB, CreateShareInput{
		RecipientUserID: lawyer, Permission: models.SharePermissionView,
	})
	require.NoError(t, err)

	page := paginationForTest(1, 20)
	result, err := svc.ListSharedWithMe(ctx, authUser(lawyer, models.RoleLawyer), page)
	require.NoError(t, err)
	require.Len(t, result.Documents, 2)
	assert.Equal(t, int64(2), result.Meta.Total)

	_, err = svc.RevokeShare(ctx, authUser(officer, models.RolePolice), docA, shareA.ShareID)
	require.NoError(t, err)

	result, err = svc.ListSharedWithMe(ctx, authUser(lawyer, models.RoleLawyer), page)
	require.NoError(t, err)
	require.Len(t, result.Documents, 1, "a revoked share must disappear from Shared With Me")
	assert.Equal(t, docB, result.Documents[0].Document.ID)
}

func TestShareService_SearchRecipients(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc := newShareServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "share-searcher27@example.com", models.RolePolice)
	newUserWithRole(t, migrator, "share-findme27@example.com", models.RoleLawyer)
	inactive := newUserWithRole(t, migrator, "share-inactive27@example.com", models.RoleLawyer)
	mustDeactivateUser(t, migrator, inactive)

	_, err := svc.SearchRecipients(ctx, authUser(officer, models.RolePolice), "a")
	require.Error(t, err, "a 1-character query must be rejected")

	results, err := svc.SearchRecipients(ctx, authUser(officer, models.RolePolice), "share-findme27")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "share-findme27@example.com", results[0].Email)

	results, err = svc.SearchRecipients(ctx, authUser(officer, models.RolePolice), "share-inactive27")
	require.NoError(t, err)
	assert.Empty(t, results, "an inactive user must never appear as a share-recipient candidate")

	results, err = svc.SearchRecipients(ctx, authUser(officer, models.RolePolice), "share-searcher27")
	require.NoError(t, err)
	assert.Empty(t, results, "the searching user must never be suggested as their own recipient")
}
