//go:build integration

// Run with: go test -tags=integration ./internal/service/...
// Requires the docker-compose postgres AND minio services up, migrated,
// seeded. See auth_service_integration_test.go's doc comment for the
// shared -p 1 note when running alongside other packages' integration
// tests, and internal/storage/minio_integration_test.go for the MinIO
// connectivity testDocumentStorage below mirrors.
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
	"evidentia/backend/internal/config"
	"evidentia/backend/internal/models"
	"evidentia/backend/internal/storage"
	"evidentia/backend/pkg/hash"
)

// testUploadMaxSize is a generous default for these small fixtures — see
// TestDocumentService_UploadDocument_OversizedFileRejectedAndNoOrphanLeft
// for the dedicated small-limit test.
const testUploadMaxSize = 10 << 20 // 10 MiB

// testDocumentStorage connects to the real docker-compose MinIO instance,
// using a dedicated test bucket (mirroring
// internal/storage/minio_integration_test.go's own convention) so these
// tests never touch the bucket a manually-run server would use.
func testDocumentStorage(t *testing.T) (storage.Storage, string) {
	t.Helper()
	bucket := envOr("MINIO_BUCKET", "evidentia-documents-test")
	cfg := config.MinIOConfig{
		Endpoint:  envOr("MINIO_ENDPOINT", "localhost:9000"),
		AccessKey: envOr("MINIO_ACCESS_KEY", "evidentia_minio"),
		SecretKey: envOr("MINIO_SECRET_KEY", "changeme_example"),
		UseSSL:    false,
		Bucket:    bucket,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := storage.NewMinIO(ctx, cfg)
	require.NoError(t, err)
	require.NoError(t, s.HealthCheck(ctx))
	return s, bucket
}

// newDocumentServiceForTest wires a real DocumentService against the live
// docker-compose PostgreSQL (via appPool) and MinIO, with a generous
// default upload limit — tests that need a different limit build their
// own DocumentService directly instead (see the oversized-file test).
func newDocumentServiceForTest(t *testing.T, recorder audit.Recorder) (*DocumentService, storage.Storage) {
	t.Helper()
	appDB := appPool(t)
	authzService := authz.NewService(appDB, recorder)
	objStorage, bucket := testDocumentStorage(t)
	svc := NewDocumentService(appDB, authzService, recorder, objStorage, bucket, testUploadMaxSize, discardLogger())
	return svc, objStorage
}

// mustSeedCase inserts a case directly (bypassing CaseService, the same
// convention backend/tests/db_schema_test.go's mustInsertCase uses) owned
// by ownerID, and bootstraps ownerID's OWNER case_members row — mirroring
// what CaseService.CreateCase does in production (see
// backend/tests/db_rls_test.go's TestRLS_CaseCreatorCanBootstrapOwnMembership
// for why that row matters to authz.CanAccessCase).
func mustSeedCase(t *testing.T, pool *pgxpool.Pool, caseNumber string, ownerID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	var caseID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO cases (case_number, title, created_by) VALUES ($1, $1, $2) RETURNING id`,
		caseNumber, ownerID,
	).Scan(&caseID))

	_, err := pool.Exec(ctx,
		`INSERT INTO case_members (case_id, user_id, membership_type, added_by) VALUES ($1, $2, 'OWNER', $2)`,
		caseID, ownerID)
	require.NoError(t, err)
	return caseID
}

func mustAddCaseMember(t *testing.T, pool *pgxpool.Pool, caseID, userID, addedBy uuid.UUID, membershipType string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO case_members (case_id, user_id, membership_type, added_by) VALUES ($1, $2, $3, $4)`,
		caseID, userID, membershipType, addedBy)
	require.NoError(t, err)
}

func TestDocumentService_UploadDocument_PoliceAllowedAndHashCorrect(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	rec := &spyRecorder{}
	svc, objStorage := newDocumentServiceForTest(t, rec)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "doc-officer1@example.com", models.RolePolice)
	caseID := mustSeedCase(t, migrator, "DOC-CASE-1", officer)

	content := []byte("this is the raw evidence file content, byte for byte")
	wantHash, err := hash.Sum256Hex(bytes.NewReader(content))
	require.NoError(t, err)

	desc := "Initial FIR scan"
	summary, err := svc.UploadDocument(ctx, authUser(officer, models.RolePolice), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeFIR,
		Description:  &desc,
		Filename:     "fir-scan.pdf",
		File:         bytes.NewReader(content),
	})
	require.NoError(t, err)

	assert.Equal(t, caseID, summary.CaseID)
	assert.Equal(t, models.DocumentTypeFIR, summary.DocumentType)
	assert.Equal(t, "fir-scan.pdf", summary.Filename)
	assert.Equal(t, int64(len(content)), summary.FileSize)
	assert.Equal(t, wantHash, summary.Sha256Hash, "the persisted hash must represent exactly the raw uploaded bytes")
	assert.Equal(t, models.DocumentStatusActive, summary.Status)
	assert.Equal(t, officer, summary.UploadedBy, "uploader must be derived from authentication, never a request field")
	assert.Contains(t, rec.actions(), "DOCUMENT_UPLOADED")

	// The object is genuinely retrievable from MinIO with the exact bytes.
	stored, err := objStorage.Get(ctx, "cases/"+caseID.String()+"/documents/"+summary.ID.String()+"/original")
	require.NoError(t, err)
	defer stored.Close()
	got, err := io.ReadAll(stored)
	require.NoError(t, err)
	assert.Equal(t, content, got)
}

func TestDocumentService_UploadDocument_LawyerDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	rec := &spyRecorder{}
	svc, _ := newDocumentServiceForTest(t, rec)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "doc-officer2@example.com", models.RolePolice)
	lawyer := newUserWithRole(t, migrator, "doc-lawyer2@example.com", models.RoleLawyer)
	caseID := mustSeedCase(t, migrator, "DOC-CASE-2", officer)
	mustAddCaseMember(t, migrator, caseID, lawyer, officer, models.MembershipTypeLawyer)

	_, err := svc.UploadDocument(ctx, authUser(lawyer, models.RoleLawyer), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeOther,
		Filename:     "should-not-upload.txt",
		File:         bytes.NewReader([]byte("x")),
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status, "LAWYER holds no document:upload permission even when attached to the case")
	assert.NotContains(t, rec.actions(), "DOCUMENT_UPLOADED")
}

func TestDocumentService_UploadDocument_UnrelatedPoliceDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	rec := &spyRecorder{}
	svc, _ := newDocumentServiceForTest(t, rec)
	ctx := context.Background()

	officerA := newUserWithRole(t, migrator, "doc-officerA3@example.com", models.RolePolice)
	officerB := newUserWithRole(t, migrator, "doc-officerB3@example.com", models.RolePolice)
	caseID := mustSeedCase(t, migrator, "DOC-CASE-3", officerA)

	_, err := svc.UploadDocument(ctx, authUser(officerB, models.RolePolice), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeOther,
		Filename:     "x.txt",
		File:         bytes.NewReader([]byte("x")),
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status, "holding document:upload does not imply access to another officer's case")
}

func TestDocumentService_UploadDocument_InvalidDocumentTypeRejected(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "doc-officer4@example.com", models.RolePolice)
	caseID := mustSeedCase(t, migrator, "DOC-CASE-4", officer)

	_, err := svc.UploadDocument(ctx, authUser(officer, models.RolePolice), caseID, UploadDocumentInput{
		DocumentType: "NOT_A_REAL_TYPE",
		Filename:     "x.txt",
		File:         bytes.NewReader([]byte("x")),
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 400, appErr.Status)
}

func TestDocumentService_UploadDocument_OversizedFileRejectedAndNoOrphanLeft(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	appDB := appPool(t)
	rec := &spyRecorder{}
	authzService := authz.NewService(appDB, rec)
	objStorage, bucket := testDocumentStorage(t)
	// A tiny limit so the fixture content deterministically exceeds it
	// without needing a large test payload.
	svc := NewDocumentService(appDB, authzService, rec, objStorage, bucket, 16, discardLogger())
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "doc-officer5@example.com", models.RolePolice)
	caseID := mustSeedCase(t, migrator, "DOC-CASE-5", officer)

	oversized := bytes.Repeat([]byte("a"), 1024)
	_, err := svc.UploadDocument(ctx, authUser(officer, models.RolePolice), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeOther,
		Filename:     "big.bin",
		File:         bytes.NewReader(oversized),
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 413, appErr.Status)
	assert.NotContains(t, rec.actions(), "DOCUMENT_UPLOADED")

	// No document row was created — nothing to look up.
	var count int
	require.NoError(t, appDB.QueryRow(ctx, `SELECT count(*) FROM documents WHERE case_id = $1`, caseID).Scan(&count))
	assert.Equal(t, 0, count)
}

func TestDocumentService_UploadDocument_FilenamePathTraversalSanitized(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "doc-officer6@example.com", models.RolePolice)
	caseID := mustSeedCase(t, migrator, "DOC-CASE-6", officer)

	summary, err := svc.UploadDocument(ctx, authUser(officer, models.RolePolice), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeOther,
		Filename:     "../../../etc/passwd",
		File:         bytes.NewReader([]byte("x")),
	})
	require.NoError(t, err)
	assert.Equal(t, "passwd", summary.Filename)
	assert.NotContains(t, summary.Filename, "..")
}

func TestDocumentService_DownloadDocument_AuthorizedSucceedsAndAudited(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	rec := &spyRecorder{}
	svc, _ := newDocumentServiceForTest(t, rec)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "doc-officer7@example.com", models.RolePolice)
	caseID := mustSeedCase(t, migrator, "DOC-CASE-7", officer)

	content := []byte("download me")
	uploaded, err := svc.UploadDocument(ctx, authUser(officer, models.RolePolice), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeOther,
		Filename:     "downloadable.txt",
		File:         bytes.NewReader(content),
	})
	require.NoError(t, err)

	rec.events = nil
	result, err := svc.DownloadDocument(ctx, authUser(officer, models.RolePolice), uploaded.ID)
	require.NoError(t, err)
	defer result.Content.Close()

	got, err := io.ReadAll(result.Content)
	require.NoError(t, err)
	assert.Equal(t, content, got)
	assert.Equal(t, "downloadable.txt", result.Document.Filename)
	assert.Contains(t, rec.actions(), "DOCUMENT_DOWNLOADED")
}

func TestDocumentService_DownloadDocument_CrossCaseLawyerDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officerA := newUserWithRole(t, migrator, "doc-officerA8@example.com", models.RolePolice)
	officerB := newUserWithRole(t, migrator, "doc-officerB8@example.com", models.RolePolice)
	lawyer := newUserWithRole(t, migrator, "doc-lawyer8@example.com", models.RoleLawyer)

	caseA := mustSeedCase(t, migrator, "DOC-CASE-8A", officerA)
	caseB := mustSeedCase(t, migrator, "DOC-CASE-8B", officerB)
	mustAddCaseMember(t, migrator, caseB, lawyer, officerB, models.MembershipTypeLawyer)

	docA, err := svc.UploadDocument(ctx, authUser(officerA, models.RolePolice), caseA, UploadDocumentInput{
		DocumentType: models.DocumentTypeOther,
		Filename:     "a.txt",
		File:         bytes.NewReader([]byte("a")),
	})
	require.NoError(t, err)

	_, err = svc.DownloadDocument(ctx, authUser(lawyer, models.RoleLawyer), docA.ID)
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status, "a lawyer assigned to case B must not access a document on case A")
}

func TestDocumentService_DownloadDocument_ForensicsCrossCaseDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officerA := newUserWithRole(t, migrator, "doc-officerA9@example.com", models.RolePolice)
	officerB := newUserWithRole(t, migrator, "doc-officerB9@example.com", models.RolePolice)
	forensics := newUserWithRole(t, migrator, "doc-forensics9@example.com", models.RoleForensics)

	caseA := mustSeedCase(t, migrator, "DOC-CASE-9A", officerA)
	caseB := mustSeedCase(t, migrator, "DOC-CASE-9B", officerB)
	mustAddCaseMember(t, migrator, caseB, forensics, officerB, models.MembershipTypeForensics)

	docA, err := svc.UploadDocument(ctx, authUser(officerA, models.RolePolice), caseA, UploadDocumentInput{
		DocumentType: models.DocumentTypeOther,
		Filename:     "a.txt",
		File:         bytes.NewReader([]byte("a")),
	})
	require.NoError(t, err)

	_, err = svc.DownloadDocument(ctx, authUser(forensics, models.RoleForensics), docA.ID)
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status)
}

func TestDocumentService_DownloadDocument_GuessedUUIDDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, _ := newDocumentServiceForTest(t, &spyRecorder{})

	officer := newUserWithRole(t, migrator, "doc-officer10@example.com", models.RolePolice)

	_, err := svc.DownloadDocument(context.Background(), authUser(officer, models.RolePolice), uuid.New())
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status, "a guessed, nonexistent document ID must be denied identically to an unrelated one")
}

func TestDocumentService_UploadDocument_AdminAllowed(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "doc-officer11@example.com", models.RolePolice)
	admin := newUserWithRole(t, migrator, "doc-admin11@example.com", models.RoleAdmin)
	caseID := mustSeedCase(t, migrator, "DOC-CASE-11", officer)

	summary, err := svc.UploadDocument(ctx, authUser(admin, models.RoleAdmin), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypeForensicReport,
		Filename:     "report.pdf",
		File:         bytes.NewReader([]byte("report bytes")),
	})
	require.NoError(t, err, "ADMIN can upload to any case per policy")
	assert.Equal(t, admin, summary.UploadedBy)
}

func TestDocumentService_UploadDocument_ForensicsAllowedOnLinkedCase(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	svc, _ := newDocumentServiceForTest(t, &spyRecorder{})
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "doc-officer12@example.com", models.RolePolice)
	forensics := newUserWithRole(t, migrator, "doc-forensics12@example.com", models.RoleForensics)
	caseID := mustSeedCase(t, migrator, "DOC-CASE-12", officer)
	mustAddCaseMember(t, migrator, caseID, forensics, officer, models.MembershipTypeForensics)

	_, err := svc.UploadDocument(ctx, authUser(forensics, models.RoleForensics), caseID, UploadDocumentInput{
		DocumentType: models.DocumentTypePhotoEvidence,
		Filename:     "photo.jpg",
		File:         bytes.NewReader([]byte("photo bytes")),
	})
	require.NoError(t, err)
}
