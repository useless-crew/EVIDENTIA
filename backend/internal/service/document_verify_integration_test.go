//go:build integration

// Run with: go test -tags=integration ./internal/service/...
// Requires the docker-compose postgres AND minio services up, migrated
// (including 000003_certificate_integrity), seeded. See
// document_service_integration_test.go for the shared helpers
// (newDocumentServiceForTest, mustSeedCase, mustAddCaseMember,
// testDocumentStorage) this file reuses, and
// auth_service_integration_test.go for the -p 1 note.
package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/models"
)

// uploadTestDocument is a small helper shared by this file's tests:
// uploads content as officer and returns the resulting summary.
func uploadTestDocument(t *testing.T, svc *DocumentService, officer, caseID uuid.UUID, filename string, content []byte) DocumentSummary {
	t.Helper()
	summary, err := svc.UploadDocument(context.Background(), authUser(officer, models.RolePolice), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeOther,
		Filename:     filename,
		File:         bytes.NewReader(content),
	})
	require.NoError(t, err)
	return *summary
}

func TestDocumentService_VerifyDocument_CorrectObjectReturnsVerified(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	rec := &spyRecorder{}
	svc, _ := newDocumentServiceForTest(t, rec)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "verify-officer1@example.com", models.RolePolice)
	caseID := mustSeedCase(t, migrator, "VERIFY-CASE-1", officer)
	content := []byte("original evidence bytes, unmodified")
	doc := uploadTestDocument(t, svc, officer, caseID, "evidence.txt", content)

	rec.events = nil
	result, err := svc.VerifyDocument(ctx, authUser(officer, models.RolePolice), doc.ID)
	require.NoError(t, err)
	assert.Equal(t, VerificationStatusVerified, result.Status)
	assert.Equal(t, doc.Sha256Hash, result.StoredHash)
	assert.Equal(t, doc.Sha256Hash, result.ComputedHash)
	assert.NotZero(t, result.VerifiedAt)
	assert.Contains(t, rec.actions(), "DOCUMENT_VERIFIED")
	assert.NotContains(t, rec.actions(), "DOCUMENT_INTEGRITY_FAILURE")
}

func TestDocumentService_VerifyDocument_ModifiedObjectReturnsIntegrityFailure(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	rec := &spyRecorder{}
	svc, objStorage := newDocumentServiceForTest(t, rec)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "verify-officer2@example.com", models.RolePolice)
	caseID := mustSeedCase(t, migrator, "VERIFY-CASE-2", officer)
	doc := uploadTestDocument(t, svc, officer, caseID, "evidence.txt", []byte("original evidence"))

	// Tamper: overwrite the stored object directly, bypassing the
	// application entirely (simulating corruption/tampering at the
	// storage layer) — the canonical hash in PostgreSQL is untouched.
	objectKey := "cases/" + caseID.String() + "/documents/" + doc.ID.String() + "/original"
	require.NoError(t, objStorage.Put(ctx, objectKey, bytes.NewReader([]byte("modified evidence")), -1, "text/plain"))

	rec.events = nil
	result, err := svc.VerifyDocument(ctx, authUser(officer, models.RolePolice), doc.ID)
	require.NoError(t, err, "an integrity mismatch is a successful verification call, not an error")
	assert.Equal(t, VerificationStatusIntegrityFailure, result.Status)
	assert.Equal(t, doc.Sha256Hash, result.StoredHash, "the canonical hash in the response must be unchanged")
	assert.NotEqual(t, result.StoredHash, result.ComputedHash)
	assert.Contains(t, rec.actions(), "DOCUMENT_INTEGRITY_FAILURE")
	assert.NotContains(t, rec.actions(), "DOCUMENT_VERIFIED")

	// The canonical hash in PostgreSQL must never have been rewritten.
	fresh, err := svc.DownloadDocument(ctx, authUser(officer, models.RolePolice), doc.ID)
	require.NoError(t, err)
	defer fresh.Content.Close()
	assert.Equal(t, doc.Sha256Hash, hex.EncodeToString(fresh.Document.Sha256Hash))
	assert.Equal(t, models.DocumentStatusTampered, fresh.Document.Status, "a discovered mismatch must mark the document TAMPERED")

	got, err := io.ReadAll(fresh.Content)
	require.NoError(t, err)
	assert.Equal(t, "modified evidence", string(got), "the object itself must also be left exactly as found — verification never repairs it")
}

func TestDocumentService_VerifyDocument_MissingObjectReturnsStorageError(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, objStorage := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "verify-officer3@example.com", models.RolePolice)
	caseID := mustSeedCase(t, migrator, "VERIFY-CASE-3", officer)
	doc := uploadTestDocument(t, svc, officer, caseID, "evidence.txt", []byte("evidence"))

	objectKey := "cases/" + caseID.String() + "/documents/" + doc.ID.String() + "/original"
	require.NoError(t, objStorage.Delete(ctx, objectKey))

	_, err := svc.VerifyDocument(ctx, authUser(officer, models.RolePolice), doc.ID)
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 503, appErr.Status, "a storage failure must be a service error, never reported as an INTEGRITY_FAILURE status")
}

func TestDocumentService_VerifyDocument_LawyerDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	rec := &spyRecorder{}
	svc, _ := newDocumentServiceForTest(t, rec)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "verify-officer4@example.com", models.RolePolice)
	lawyer := newUserWithRole(t, migrator, "verify-lawyer4@example.com", models.RoleLawyer)
	caseID := mustSeedCase(t, migrator, "VERIFY-CASE-4", officer)
	mustAddCaseMember(t, migrator, caseID, lawyer, officer, models.MembershipTypeLawyer)
	doc := uploadTestDocument(t, svc, officer, caseID, "evidence.txt", []byte("evidence"))

	_, err := svc.VerifyDocument(ctx, authUser(lawyer, models.RoleLawyer), doc.ID)
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status, "LAWYER holds no document:verify permission even when attached to the case")
}

func TestDocumentService_VerifyDocument_JudgeDenied(t *testing.T) {
	// Per the seed data, JUDGE holds document:read/download and
	// certificate:read, but NOT document:verify — verification is denied
	// at the RBAC gate regardless of any docket/case relationship.
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "verify-officer5@example.com", models.RolePolice)
	judge := newUserWithRole(t, migrator, "verify-judge5@example.com", models.RoleJudge)
	caseID := mustSeedCase(t, migrator, "VERIFY-CASE-5", officer)
	mustAddCaseMember(t, migrator, caseID, judge, officer, models.MembershipTypeJudge)
	doc := uploadTestDocument(t, svc, officer, caseID, "evidence.txt", []byte("evidence"))

	_, err := svc.VerifyDocument(ctx, authUser(judge, models.RoleJudge), doc.ID)
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status)
}

func TestDocumentService_VerifyDocument_UnrelatedPoliceDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officerA := newUserWithRole(t, migrator, "verify-officerA6@example.com", models.RolePolice)
	officerB := newUserWithRole(t, migrator, "verify-officerB6@example.com", models.RolePolice)
	caseID := mustSeedCase(t, migrator, "VERIFY-CASE-6", officerA)
	doc := uploadTestDocument(t, svc, officerA, caseID, "evidence.txt", []byte("evidence"))

	_, err := svc.VerifyDocument(ctx, authUser(officerB, models.RolePolice), doc.ID)
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status, "holding document:verify does not imply access to another officer's case")
}

func TestDocumentService_VerifyDocument_GuessedUUIDDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, _ := newDocumentServiceForTest(t, &spyRecorder{})

	officer := newUserWithRole(t, migrator, "verify-officer7@example.com", models.RolePolice)

	_, err := svc.VerifyDocument(context.Background(), authUser(officer, models.RolePolice), uuid.New())
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status, "a guessed, nonexistent document ID must be denied identically to an unrelated one")
}

func TestDocumentService_VerifyDocument_AdminAllowed(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "verify-officer8@example.com", models.RolePolice)
	admin := newUserWithRole(t, migrator, "verify-admin8@example.com", models.RoleAdmin)
	caseID := mustSeedCase(t, migrator, "VERIFY-CASE-8", officer)
	doc := uploadTestDocument(t, svc, officer, caseID, "evidence.txt", []byte("evidence"))

	result, err := svc.VerifyDocument(ctx, authUser(admin, models.RoleAdmin), doc.ID)
	require.NoError(t, err)
	assert.Equal(t, VerificationStatusVerified, result.Status)
}

func TestDocumentService_VerifyDocument_RepeatedVerificationIsIdempotent(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "verify-officer9@example.com", models.RolePolice)
	caseID := mustSeedCase(t, migrator, "VERIFY-CASE-9", officer)
	doc := uploadTestDocument(t, svc, officer, caseID, "evidence.txt", []byte("evidence"))
	user := authUser(officer, models.RolePolice)

	first, err := svc.VerifyDocument(ctx, user, doc.ID)
	require.NoError(t, err)
	second, err := svc.VerifyDocument(ctx, user, doc.ID)
	require.NoError(t, err)

	assert.Equal(t, first.Status, second.Status)
	assert.Equal(t, first.StoredHash, second.StoredHash)
	assert.Equal(t, first.ComputedHash, second.ComputedHash)
}

func TestDocumentService_VerifyDocument_ReVerificationAfterRestoreClearsTamperedStatus(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, objStorage := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "verify-officer10@example.com", models.RolePolice)
	caseID := mustSeedCase(t, migrator, "VERIFY-CASE-10", officer)
	original := []byte("original evidence")
	doc := uploadTestDocument(t, svc, officer, caseID, "evidence.txt", original)
	user := authUser(officer, models.RolePolice)
	objectKey := "cases/" + caseID.String() + "/documents/" + doc.ID.String() + "/original"

	require.NoError(t, objStorage.Put(ctx, objectKey, bytes.NewReader([]byte("tampered")), -1, "text/plain"))
	result, err := svc.VerifyDocument(ctx, user, doc.ID)
	require.NoError(t, err)
	require.Equal(t, VerificationStatusIntegrityFailure, result.Status)

	// Restore the exact original bytes (e.g. a hypothetical operator
	// recovering from backup) — re-verification must reflect the CURRENT
	// truth, not a permanently "stuck" tampered flag.
	require.NoError(t, objStorage.Put(ctx, objectKey, bytes.NewReader(original), -1, "text/plain"))
	result, err = svc.VerifyDocument(ctx, user, doc.ID)
	require.NoError(t, err)
	assert.Equal(t, VerificationStatusVerified, result.Status)

	downloaded, err := svc.DownloadDocument(ctx, user, doc.ID)
	require.NoError(t, err)
	defer downloaded.Content.Close()
	assert.Equal(t, models.DocumentStatusActive, downloaded.Document.Status)
}

func hexEncode(b []byte) string {
	const hextable = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hextable[v>>4]
		out[i*2+1] = hextable[v&0x0f]
	}
	return string(out)
}
