//go:build integration

// Run with: go test -tags=integration ./internal/httpserver/...
// Requires the full docker-compose infrastructure (postgres, redis, minio)
// up and migrated (including 000003_certificate_integrity)/seeded — see
// auth_flow_integration_test.go's doc comment for the shared -p 1 note.
//
// This exercises System 7's verification/certificate routes through the
// fully wired application, driven by real HTTP requests exactly as a
// client would experience them — complementing internal/service's
// DocumentService/CertificateService-level tests (which exercise the
// business logic directly, including hash correctness that's awkward to
// assert on through raw HTTP) and reusing document_flow_integration_test.go's
// doUpload/doDownload and case_flow_integration_test.go's loginAs/
// seedCaseTestUser/doJSONRaw (same package).
package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/app"
)

type verifyEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		DocumentID   string `json:"document_id"`
		Status       string `json:"status"`
		StoredHash   string `json:"stored_hash"`
		ComputedHash string `json:"computed_hash"`
		VerifiedAt   string `json:"verified_at"`
	} `json:"data"`
	Error *errorEnvelope `json:"error"`
}

type certificateEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		ID                 string `json:"id"`
		DocumentID         string `json:"document_id"`
		DocumentHash       string `json:"document_hash"`
		CertificateVersion string `json:"certificate_version"`
		SignatureAlgorithm string `json:"signature_algorithm"`
		Signature          string `json:"signature"`
		Issuer             string `json:"issuer"`
		GeneratedBy        string `json:"generated_by"`
		GeneratedAt        string `json:"generated_at"`
	} `json:"data"`
	Error *errorEnvelope `json:"error"`
}

func doVerify(t *testing.T, router http.Handler, documentID, bearer string, extraHeaders map[string]string) (*httptest.ResponseRecorder, verifyEnvelope) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/documents/"+documentID+"/verify", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var env verifyEnvelope
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	}
	return rec, env
}

func doCertificate(t *testing.T, router http.Handler, documentID, bearer string) (*httptest.ResponseRecorder, certificateEnvelope) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/documents/"+documentID+"/certificate", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var env certificateEnvelope
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	}
	return rec, env
}

func TestDocumentVerifyCertificateFlow_EndToEnd(t *testing.T) {
	setenvIfUnset(t, "DATABASE_USER", "evidentia_app")
	setenvIfUnset(t, "DATABASE_PASSWORD", "changeme_example")
	setenvIfUnset(t, "DATABASE_NAME", "evidentia")
	setenvIfUnset(t, "DATABASE_MIGRATOR_USER", "evidentia")
	setenvIfUnset(t, "DATABASE_MIGRATOR_PASSWORD", "changeme_example")
	setenvIfUnset(t, "MINIO_ACCESS_KEY", "evidentia_minio")
	setenvIfUnset(t, "MINIO_SECRET_KEY", "changeme_example")
	setenvIfUnset(t, "MINIO_BUCKET", "evidentia-documents")
	setenvIfUnset(t, "JWT_SIGNING_KEY", "test-signing-key-at-least-32-characters-long")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	application, err := app.New(ctx)
	require.NoError(t, err)
	defer application.Close()
	router := NewRouter(application)

	migrator, err := pgxpool.New(ctx, "postgres://evidentia:changeme_example@localhost:5432/evidentia?sslmode=disable")
	require.NoError(t, err)
	defer migrator.Close()

	const password = "correct horse battery staple"
	suffix := uuid.New().String()[:8]
	policeEmail := "verify-http-police-" + suffix + "@example.com"
	lawyerEmail := "verify-http-lawyer-" + suffix + "@example.com"
	adminEmail := "verify-http-admin-" + suffix + "@example.com"
	judgeEmail := "verify-http-judge-" + suffix + "@example.com"

	seedCaseTestUser(t, migrator, policeEmail, "POLICE", password)
	seedCaseTestUser(t, migrator, lawyerEmail, "LAWYER", password)
	seedCaseTestUser(t, migrator, adminEmail, "ADMIN", password)
	seedCaseTestUser(t, migrator, judgeEmail, "JUDGE", password)
	policeToken := loginAs(t, router, policeEmail, password)
	lawyerToken := loginAs(t, router, lawyerEmail, password)
	adminToken := loginAs(t, router, adminEmail, password)
	judgeToken := loginAs(t, router, judgeEmail, password)

	rec, caseResp := doJSONWithHeaders(t, router, http.MethodPost, "/api/v1/cases", map[string]any{
		"case_number": "VERIFY-HTTP-" + suffix, "title": "Verify/certificate flow case",
	}, policeToken, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	caseID := caseResp.Data.ID
	require.NotEmpty(t, caseID)

	// ---- Document A: verification flow (correct then tampered) ----
	contentA := []byte("evidence file content for the verify HTTP flow test")
	rec, uploadA := doUpload(t, router, caseID, policeToken, "OTHER", "", "evidence-a.txt", contentA, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	docAID := uploadA.Data.ID

	// 1. Unauthenticated verify rejected.
	rec, _ = doVerify(t, router, docAID, "", nil)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// 2. LAWYER holds no document:verify permission.
	rec, lawyerVerify := doVerify(t, router, docAID, lawyerToken, nil)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.NotNil(t, lawyerVerify.Error)

	// 3. X-User-ID/X-Role header spoofing has no effect on the RBAC/ABAC
	// decision — still denied as the real (LAWYER) identity.
	rec, spoofed := doVerify(t, router, docAID, lawyerToken, map[string]string{
		"X-User-ID": uuid.New().String(),
		"X-Role":    "ADMIN",
	})
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "FORBIDDEN", spoofed.Error.Code)

	// 4. POLICE verifies the untouched object: VERIFIED, stored == computed
	// == the hash returned at upload time.
	rec, verified := doVerify(t, router, docAID, policeToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, verified.Success)
	require.Equal(t, "VERIFIED", verified.Data.Status)
	require.Equal(t, uploadA.Data.Sha256Hash, verified.Data.StoredHash)
	require.Equal(t, uploadA.Data.Sha256Hash, verified.Data.ComputedHash)
	require.NotContains(t, rec.Body.String(), "minio", "no storage backend detail may leak into the response")

	// 5. Tamper the stored object directly (bypassing the application, as
	// corruption/tampering at the storage layer would) — re-verification
	// must report INTEGRITY_FAILURE as a normal 200, with the canonical
	// (stored) hash unchanged and the computed hash now different. This is
	// still a 200: verification SUCCEEDED at answering the question, even
	// though what it found is a failure.
	objectKeyA := "cases/" + caseID + "/documents/" + docAID + "/original"
	require.NoError(t, application.Storage.Put(ctx, objectKeyA, bytes.NewReader([]byte("tampered bytes")), -1, "text/plain"))

	rec, tampered := doVerify(t, router, docAID, policeToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "INTEGRITY_FAILURE", tampered.Data.Status)
	require.Equal(t, uploadA.Data.Sha256Hash, tampered.Data.StoredHash, "the canonical hash must never change on a detected mismatch")
	require.NotEqual(t, tampered.Data.StoredHash, tampered.Data.ComputedHash)

	// ---- Document B: certificate flow (kept untouched) ----
	contentB := []byte("evidence file content for the certificate HTTP flow test")
	rec, uploadB := doUpload(t, router, caseID, policeToken, "OTHER", "", "evidence-b.txt", contentB, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	docBID := uploadB.Data.ID

	// 6. Unauthenticated certificate read rejected.
	rec, _ = doCertificate(t, router, docBID, "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// 7. POLICE holds neither certificate:read nor certificate:create.
	rec, policeCert := doCertificate(t, router, docBID, policeToken)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.NotNil(t, policeCert.Error)

	// 8. IDOR: JUDGE holds certificate:read (RBAC) but is not a member of
	// this case (ABAC) — still denied.
	rec, _ = doCertificate(t, router, docBID, judgeToken)
	require.Equal(t, http.StatusForbidden, rec.Code)

	// 9. ADMIN requests the certificate: none exists yet, so one is
	// generated on demand, bound to the exact uploaded hash, and signed.
	rec, created := doCertificate(t, router, docBID, adminToken)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, created.Success)
	require.Equal(t, docBID, created.Data.DocumentID)
	require.Equal(t, uploadB.Data.Sha256Hash, created.Data.DocumentHash)
	require.NotEmpty(t, created.Data.Signature)
	require.Equal(t, "ECDSA-P256-SHA256", created.Data.SignatureAlgorithm)
	require.NotContains(t, rec.Body.String(), "minio", "no storage backend detail may leak into the response")

	// 10. A second request returns the SAME certificate — generation is
	// idempotent, never a duplicate.
	rec, again := doCertificate(t, router, docBID, adminToken)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, created.Data.ID, again.Data.ID)

	// 11. A tampered document must never receive a valid certificate
	// (docA is already tampered, from step 5 above).
	rec, conflictResp := doCertificate(t, router, docAID, adminToken)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.NotNil(t, conflictResp.Error)
}
