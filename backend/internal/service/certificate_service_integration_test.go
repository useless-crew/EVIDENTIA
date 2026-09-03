//go:build integration

// Run with: go test -tags=integration ./internal/service/...
// Requires the docker-compose postgres AND minio services up, migrated
// (including 000003_certificate_integrity), seeded. Reuses
// document_service_integration_test.go's newDocumentServiceForTest/
// testDocumentStorage/mustSeedCase/mustAddCaseMember,
// document_verify_integration_test.go's uploadTestDocument, and
// case_service_integration_test.go's spyRecorder/authUser/newUserWithRole/
// truncateCaseTables/utilsAsAppError — see auth_service_integration_test.go
// for the -p 1 note.
package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"evidentia/backend/db/generated"
	"evidentia/backend/internal/audit"
	"evidentia/backend/internal/authz"
	"evidentia/backend/internal/models"
	"evidentia/backend/internal/repository"
)

// newCertificateServiceForTest wires a real CertificateService against the
// live docker-compose PostgreSQL/MinIO. No CERTIFICATE_SIGNING_KEY is
// configured, so NewCertificateService generates a fresh, process-lifetime
// ECDSA key (see that constructor's doc comment) — sufficient here since
// every test signs and verifies within the same process.
func newCertificateServiceForTest(t *testing.T, recorder audit.Recorder) *CertificateService {
	t.Helper()
	appDB := appPool(t)
	authzService := authz.NewService(appDB, recorder)
	objStorage, _ := testDocumentStorage(t)
	svc, err := NewCertificateService(appDB, authzService, recorder, objStorage, "", discardLogger())
	require.NoError(t, err)
	return svc
}

// fetchCertificateRow reads the raw generated.ComplianceCertificate row
// (document_hash is hex-decoded from CertificateSummary.DocumentHash) for
// tests that need to inspect/mutate certificate_data directly — something
// CertificateSummary deliberately doesn't expose.
func fetchCertificateRow(t *testing.T, documentID uuid.UUID, documentHashHex string) generated.ComplianceCertificate {
	t.Helper()
	appDB := appPool(t)
	hashBytes, err := hex.DecodeString(documentHashHex)
	require.NoError(t, err)

	var cert generated.ComplianceCertificate
	ident := repository.AppIdentity{UserID: uuid.Nil, Role: models.RoleAdmin}
	err = repository.WithTx(context.Background(), appDB, ident, func(ctx context.Context, q *generated.Queries) error {
		c, err := repository.NewCertificateRepo(q).GetByDocumentAndHash(ctx, documentID, hashBytes)
		cert = c
		return err
	})
	require.NoError(t, err)
	return cert
}

func TestCertificateService_GetOrCreateCertificate_AdminGeneratesForValidDocument(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	rec := &spyRecorder{}
	docSvc, _ := newDocumentServiceForTest(t, rec)
	certSvc := newCertificateServiceForTest(t, rec)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "cert-officer1@example.com", models.RolePolice)
	admin := newUserWithRole(t, migrator, "cert-admin1@example.com", models.RoleAdmin)
	caseID := mustSeedCase(t, migrator, "CERT-CASE-1", officer)
	doc := uploadTestDocument(t, docSvc, officer, caseID, "evidence.txt", []byte("certificate evidence bytes"))

	rec.events = nil
	summary, err := certSvc.GetOrCreateCertificate(ctx, authUser(admin, models.RoleAdmin), doc.ID)
	require.NoError(t, err)
	assert.Equal(t, doc.ID, summary.DocumentID)
	assert.Equal(t, doc.Sha256Hash, summary.DocumentHash, "the certificate must be bound to the EXACT document hash")
	assert.Equal(t, admin, summary.GeneratedBy)
	assert.NotEmpty(t, summary.Signature)
	assert.Equal(t, "ECDSA-P256-SHA256", summary.SignatureAlgorithm)
	assert.NotZero(t, summary.GeneratedAt)
	assert.Contains(t, rec.actions(), "CERTIFICATE_CREATED")
}

func TestCertificateService_GetOrCreateCertificate_TamperedDocumentRefused(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	rec := &spyRecorder{}
	docSvc, objStorage := newDocumentServiceForTest(t, rec)
	certSvc := newCertificateServiceForTest(t, rec)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "cert-officer2@example.com", models.RolePolice)
	admin := newUserWithRole(t, migrator, "cert-admin2@example.com", models.RoleAdmin)
	caseID := mustSeedCase(t, migrator, "CERT-CASE-2", officer)
	doc := uploadTestDocument(t, docSvc, officer, caseID, "evidence.txt", []byte("original evidence"))

	objectKey := "cases/" + caseID.String() + "/documents/" + doc.ID.String() + "/original"
	require.NoError(t, objStorage.Put(ctx, objectKey, bytes.NewReader([]byte("tampered evidence")), -1, "text/plain"))

	rec.events = nil
	_, err := certSvc.GetOrCreateCertificate(ctx, authUser(admin, models.RoleAdmin), doc.ID)
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 409, appErr.Status, "a tampered document must never receive a valid certificate")
	assert.Contains(t, rec.actions(), "DOCUMENT_INTEGRITY_FAILURE")
	assert.NotContains(t, rec.actions(), "CERTIFICATE_CREATED")

	// Confirm no certificate row exists: a JUDGE (certificate:read only,
	// no certificate:create) must see 404, not a stale/partial certificate.
	judge := newUserWithRole(t, migrator, "cert-judge2@example.com", models.RoleJudge)
	mustAddCaseMember(t, migrator, caseID, judge, officer, models.MembershipTypeJudge)
	_, err = certSvc.GetOrCreateCertificate(ctx, authUser(judge, models.RoleJudge), doc.ID)
	require.Error(t, err)
	appErr, ok = utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 404, appErr.Status)
}

func TestCertificateService_GetOrCreateCertificate_ConcurrentGenerationProducesOneCertificate(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	rec := &spyRecorder{}
	docSvc, _ := newDocumentServiceForTest(t, rec)
	certSvc := newCertificateServiceForTest(t, rec)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "cert-officer3@example.com", models.RolePolice)
	admin := newUserWithRole(t, migrator, "cert-admin3@example.com", models.RoleAdmin)
	caseID := mustSeedCase(t, migrator, "CERT-CASE-3", officer)
	doc := uploadTestDocument(t, docSvc, officer, caseID, "evidence.txt", []byte("concurrent evidence"))

	const n = 5
	results := make([]*CertificateSummary, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = certSvc.GetOrCreateCertificate(ctx, authUser(admin, models.RoleAdmin), doc.ID)
		}(i)
	}
	wg.Wait()

	var certID string
	for i := 0; i < n; i++ {
		require.NoError(t, errs[i])
		require.NotNil(t, results[i])
		if certID == "" {
			certID = results[i].ID.String()
		} else {
			assert.Equal(t, certID, results[i].ID.String(), "concurrent generation must never produce more than one certificate row")
		}
	}
}

func TestCertificateService_GetOrCreateCertificate_JudgeCanReadExistingButNotGenerate(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	rec := &spyRecorder{}
	docSvc, _ := newDocumentServiceForTest(t, rec)
	certSvc := newCertificateServiceForTest(t, rec)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "cert-officer4@example.com", models.RolePolice)
	admin := newUserWithRole(t, migrator, "cert-admin4@example.com", models.RoleAdmin)
	judge := newUserWithRole(t, migrator, "cert-judge4@example.com", models.RoleJudge)
	caseID := mustSeedCase(t, migrator, "CERT-CASE-4", officer)
	mustAddCaseMember(t, migrator, caseID, judge, officer, models.MembershipTypeJudge)
	doc := uploadTestDocument(t, docSvc, officer, caseID, "evidence.txt", []byte("judge evidence"))

	// No certificate exists yet: JUDGE holds certificate:read but not
	// certificate:create, so this must be an indistinguishable 404 — never
	// leaking the create/read permission split to the client.
	_, err := certSvc.GetOrCreateCertificate(ctx, authUser(judge, models.RoleJudge), doc.ID)
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 404, appErr.Status)

	created, err := certSvc.GetOrCreateCertificate(ctx, authUser(admin, models.RoleAdmin), doc.ID)
	require.NoError(t, err)

	read, err := certSvc.GetOrCreateCertificate(ctx, authUser(judge, models.RoleJudge), doc.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, read.ID)
	assert.Equal(t, created.DocumentHash, read.DocumentHash)
}

func TestCertificateService_GetOrCreateCertificate_OfficerWithoutCertificatePermissionDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	rec := &spyRecorder{}
	docSvc, _ := newDocumentServiceForTest(t, rec)
	certSvc := newCertificateServiceForTest(t, rec)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "cert-officer5@example.com", models.RolePolice)
	caseID := mustSeedCase(t, migrator, "CERT-CASE-5", officer)
	doc := uploadTestDocument(t, docSvc, officer, caseID, "evidence.txt", []byte("evidence"))

	_, err := certSvc.GetOrCreateCertificate(ctx, authUser(officer, models.RolePolice), doc.ID)
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status, "POLICE holds neither certificate:read nor certificate:create per seed data")
}

func TestCertificateService_GetOrCreateCertificate_UnrelatedJudgeDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	rec := &spyRecorder{}
	docSvc, _ := newDocumentServiceForTest(t, rec)
	certSvc := newCertificateServiceForTest(t, rec)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "cert-officer6@example.com", models.RolePolice)
	judge := newUserWithRole(t, migrator, "cert-judge6@example.com", models.RoleJudge)
	caseID := mustSeedCase(t, migrator, "CERT-CASE-6", officer)
	doc := uploadTestDocument(t, docSvc, officer, caseID, "evidence.txt", []byte("evidence"))

	// judge is NOT a member of this case: holding certificate:read (RBAC)
	// is not enough — ABAC's case-relationship check must still deny this.
	_, err := certSvc.GetOrCreateCertificate(ctx, authUser(judge, models.RoleJudge), doc.ID)
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status, "certificate:read does not imply access to an unrelated case's document")
}

func TestCertificateService_VerifyCertificateIntegrity_ValidCertificate(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	rec := &spyRecorder{}
	docSvc, _ := newDocumentServiceForTest(t, rec)
	certSvc := newCertificateServiceForTest(t, rec)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "cert-officer7@example.com", models.RolePolice)
	admin := newUserWithRole(t, migrator, "cert-admin7@example.com", models.RoleAdmin)
	caseID := mustSeedCase(t, migrator, "CERT-CASE-7", officer)
	doc := uploadTestDocument(t, docSvc, officer, caseID, "evidence.txt", []byte("verify integrity evidence"))

	summary, err := certSvc.GetOrCreateCertificate(ctx, authUser(admin, models.RoleAdmin), doc.ID)
	require.NoError(t, err)

	certRow := fetchCertificateRow(t, doc.ID, summary.DocumentHash)
	result, err := certSvc.VerifyCertificateIntegrity(certRow, certRow.DocumentHash)
	require.NoError(t, err)
	assert.True(t, result.HashMatches)
	assert.True(t, result.SignatureChecked)
	assert.True(t, result.SignatureValid)
}

func TestCertificateService_VerifyCertificateIntegrity_HashMismatchDetected(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	rec := &spyRecorder{}
	docSvc, _ := newDocumentServiceForTest(t, rec)
	certSvc := newCertificateServiceForTest(t, rec)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "cert-officer8@example.com", models.RolePolice)
	admin := newUserWithRole(t, migrator, "cert-admin8@example.com", models.RoleAdmin)
	caseID := mustSeedCase(t, migrator, "CERT-CASE-8", officer)
	doc := uploadTestDocument(t, docSvc, officer, caseID, "evidence.txt", []byte("hash mismatch evidence"))

	summary, err := certSvc.GetOrCreateCertificate(ctx, authUser(admin, models.RoleAdmin), doc.ID)
	require.NoError(t, err)
	certRow := fetchCertificateRow(t, doc.ID, summary.DocumentHash)

	forgedHash := append([]byte(nil), certRow.DocumentHash...)
	forgedHash[0] ^= 0xFF

	result, err := certSvc.VerifyCertificateIntegrity(certRow, forgedHash)
	require.NoError(t, err)
	assert.False(t, result.HashMatches, "a certificate must never be reported valid against a hash it wasn't bound to")
}

func TestCertificateService_VerifyCertificateIntegrity_TamperedSignatureFails(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	rec := &spyRecorder{}
	docSvc, _ := newDocumentServiceForTest(t, rec)
	certSvc := newCertificateServiceForTest(t, rec)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "cert-officer9@example.com", models.RolePolice)
	admin := newUserWithRole(t, migrator, "cert-admin9@example.com", models.RoleAdmin)
	caseID := mustSeedCase(t, migrator, "CERT-CASE-9", officer)
	doc := uploadTestDocument(t, docSvc, officer, caseID, "evidence.txt", []byte("signature tamper evidence"))

	summary, err := certSvc.GetOrCreateCertificate(ctx, authUser(admin, models.RoleAdmin), doc.ID)
	require.NoError(t, err)
	certRow := fetchCertificateRow(t, doc.ID, summary.DocumentHash)

	var data struct {
		SignatureAlgorithm string `json:"signature_algorithm"`
		Signature          string `json:"signature"`
		Issuer             string `json:"issuer"`
	}
	require.NoError(t, json.Unmarshal(certRow.CertificateData, &data))
	sigBytes, err := hex.DecodeString(data.Signature)
	require.NoError(t, err)
	sigBytes[len(sigBytes)-1] ^= 0xFF
	data.Signature = hex.EncodeToString(sigBytes)
	corrupted, err := json.Marshal(data)
	require.NoError(t, err)
	certRow.CertificateData = corrupted

	result, err := certSvc.VerifyCertificateIntegrity(certRow, certRow.DocumentHash)
	require.NoError(t, err)
	assert.True(t, result.SignatureChecked)
	assert.False(t, result.SignatureValid, "a modified signature must never verify — a database record alone is not proof of validity")
}
