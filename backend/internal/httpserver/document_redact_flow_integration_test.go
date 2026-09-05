//go:build integration

// Run with: go test -tags=integration ./internal/httpserver/...
// Requires the full docker-compose infrastructure (postgres, redis, minio)
// up and migrated/seeded — see auth_flow_integration_test.go's doc comment
// for the shared -p 1 note.
//
// This exercises the redaction route through the fully wired application,
// driven by real HTTP requests exactly as a client would experience them
// — complementing internal/service's DocumentService.RedactDocument-level
// tests (document_redact_integration_test.go), which exercise the
// business logic directly. Reuses document_flow_integration_test.go's
// doUpload/doDownload and case_flow_integration_test.go's loginAs/
// seedCaseTestUser/doJSONWithHeaders (same package), plus this file's own
// doRedact.
package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/app"
)

type redactEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		RedactionID      string `json:"redaction_id"`
		SourceDocumentID string `json:"source_document_id"`
		Reason           string `json:"reason"`
		CreatedAt        string `json:"created_at"`
		Document         struct {
			ID               string  `json:"id"`
			CaseID           string  `json:"case_id"`
			DocumentType     string  `json:"document_type"`
			Filename         string  `json:"filename"`
			MimeType         string  `json:"mime_type"`
			FileSize         int64   `json:"file_size"`
			Sha256Hash       string  `json:"sha256_hash"`
			Status           string  `json:"status"`
			ParentDocumentID *string `json:"parent_document_id"`
			UploadedBy       string  `json:"uploaded_by"`
		} `json:"document"`
	} `json:"data"`
	Error *errorEnvelope `json:"error"`
}

func doRedact(t *testing.T, router http.Handler, documentID, bearer string, body map[string]any) (*httptest.ResponseRecorder, redactEnvelope) {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/documents/"+documentID+"/redact", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var env redactEnvelope
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	}
	return rec, env
}

// redactFlowTestPNG builds a small, real, decodable PNG — redaction
// requires genuine image content, unlike document_flow_integration_test.go's
// plain-text fixtures.
func redactFlowTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 30, 30))
	for y := 0; y < 30; y++ {
		for x := 0; x < 30; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 180, G: 30, B: 30, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func TestDocumentRedactFlow_EndToEnd(t *testing.T) {
	setenvIfUnset(t, "DATABASE_USER", "evidentia_app")
	setenvIfUnset(t, "DATABASE_PASSWORD", "changeme_example")
	setenvIfUnset(t, "DATABASE_NAME", "evidentia")
	setenvIfUnset(t, "DATABASE_MIGRATOR_USER", "evidentia")
	setenvIfUnset(t, "DATABASE_MIGRATOR_PASSWORD", "changeme_example")
	setenvIfUnset(t, "MINIO_ACCESS_KEY", "evidentia_minio")
	setenvIfUnset(t, "MINIO_SECRET_KEY", "changeme_example")
	setenvIfUnset(t, "MINIO_BUCKET", "evidentia-documents")
	setenvIfUnset(t, "REDIS_PASSWORD", "changeme_example")
	setenvIfUnset(t, "LOGIN_RATE_LIMIT_IP_MAX", "1000000")
	setenvIfUnset(t, "LOGIN_RATE_LIMIT_ACCOUNT_MAX", "1000000")
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
	policeEmail := "redact-http-police-" + suffix + "@example.com"
	lawyerEmail := "redact-http-lawyer-" + suffix + "@example.com"
	adminEmail := "redact-http-admin-" + suffix + "@example.com"

	seedCaseTestUser(t, migrator, policeEmail, "POLICE", password)
	seedCaseTestUser(t, migrator, lawyerEmail, "LAWYER", password)
	seedCaseTestUser(t, migrator, adminEmail, "ADMIN", password)
	policeToken := loginAs(t, router, policeEmail, password)
	lawyerToken := loginAs(t, router, lawyerEmail, password)
	adminToken := loginAs(t, router, adminEmail, password)

	rec, caseResp := doJSONWithHeaders(t, router, http.MethodPost, "/api/v1/cases", map[string]any{
		"case_number": "REDACT-HTTP-" + suffix, "title": "Redaction flow case",
	}, policeToken, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	caseID := caseResp.Data.ID
	require.NotEmpty(t, caseID)

	pngBytes := redactFlowTestPNG(t)
	rec, uploaded := doUpload(t, router, caseID, policeToken, "WITNESS_STATEMENT", "A witness photo", "witness.png", pngBytes, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	documentID := uploaded.Data.ID

	redactBody := map[string]any{
		"reason": "Protect witness identity",
		"regions": []map[string]any{
			{"page": 1, "x": 0, "y": 0, "width": 10, "height": 10},
		},
	}

	// 1. Unauthenticated redact rejected.
	rec, _ = doRedact(t, router, documentID, "", redactBody)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// 2. POLICE holds no document:redact permission (ADMIN-only today).
	rec, policeAttempt := doRedact(t, router, documentID, policeToken, redactBody)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.NotNil(t, policeAttempt.Error)

	// 3. A missing reason is rejected with 400, before any derivative is created.
	rec, _ = doRedact(t, router, documentID, adminToken, map[string]any{
		"regions": []map[string]any{{"page": 1, "x": 0, "y": 0, "width": 10, "height": 10}},
	})
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

	// 4. ADMIN redacts successfully: a brand-new, independent document.
	rec, redacted := doRedact(t, router, documentID, adminToken, redactBody)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.True(t, redacted.Success)
	require.Equal(t, documentID, redacted.Data.SourceDocumentID)
	require.Equal(t, "Protect witness identity", redacted.Data.Reason)
	require.NotEmpty(t, redacted.Data.RedactionID)
	derivativeID := redacted.Data.Document.ID
	require.NotEmpty(t, derivativeID)
	require.NotEqual(t, documentID, derivativeID)
	require.NotEqual(t, uploaded.Data.Sha256Hash, redacted.Data.Document.Sha256Hash, "H1 != H2")
	require.NotNil(t, redacted.Data.Document.ParentDocumentID)
	require.Equal(t, documentID, *redacted.Data.Document.ParentDocumentID)
	require.NotContains(t, rec.Body.String(), "minio", "no storage backend detail may leak into the response")

	// 5. The ORIGINAL is byte-for-byte unchanged and still independently
	// downloadable/verifiable at its own ID.
	origDownload := doDownload(t, router, documentID, policeToken)
	require.Equal(t, http.StatusOK, origDownload.Code)
	require.Equal(t, pngBytes, origDownload.Body.Bytes(), "the original's bytes must be untouched by redaction")

	origVerifyRec, origVerify := doVerify(t, router, documentID, policeToken, nil)
	require.Equal(t, http.StatusOK, origVerifyRec.Code)
	require.Equal(t, "VERIFIED", origVerify.Data.Status)
	require.Equal(t, uploaded.Data.Sha256Hash, origVerify.Data.StoredHash, "the original's canonical hash must never change")

	// 6. The DERIVATIVE is independently downloadable/verifiable at ITS OWN
	// ID, with its own (different) hash, and gets its own certificate bound
	// to that hash.
	derivDownload := doDownload(t, router, derivativeID, policeToken)
	require.Equal(t, http.StatusOK, derivDownload.Code)
	require.NotEqual(t, pngBytes, derivDownload.Body.Bytes())

	derivVerifyRec, derivVerify := doVerify(t, router, derivativeID, policeToken, nil)
	require.Equal(t, http.StatusOK, derivVerifyRec.Code)
	require.Equal(t, "VERIFIED", derivVerify.Data.Status)
	require.Equal(t, redacted.Data.Document.Sha256Hash, derivVerify.Data.StoredHash)

	certRec, cert := doCertificate(t, router, derivativeID, adminToken)
	require.Equal(t, http.StatusOK, certRec.Code, certRec.Body.String())
	require.Equal(t, derivativeID, cert.Data.DocumentID)
	require.Equal(t, redacted.Data.Document.Sha256Hash, cert.Data.DocumentHash)

	// The original's own certificate, generated independently, is still
	// bound to H1 — never overwritten or confused with the derivative's.
	origCertRec, origCert := doCertificate(t, router, documentID, adminToken)
	require.Equal(t, http.StatusOK, origCertRec.Code, origCertRec.Body.String())
	require.Equal(t, uploaded.Data.Sha256Hash, origCert.Data.DocumentHash)
	require.NotEqual(t, origCert.Data.DocumentHash, cert.Data.DocumentHash)

	// 7. IDOR: an unrelated LAWYER cannot reach the derivative either —
	// access is controlled independently, never opened up merely because
	// the derivative exists.
	unrelatedRec := doDownload(t, router, derivativeID, lawyerToken)
	require.Equal(t, http.StatusForbidden, unrelatedRec.Code)

	// 8. A redaction request naming a document ID that does not exist is
	// denied identically (generic 403), never a distinguishable 404.
	rec, _ = doRedact(t, router, uuid.New().String(), adminToken, redactBody)
	require.Equal(t, http.StatusForbidden, rec.Code)
}
