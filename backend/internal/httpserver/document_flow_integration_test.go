//go:build integration

// Run with: go test -tags=integration ./internal/httpserver/...
// Requires the full docker-compose infrastructure (postgres, redis, minio)
// up and migrated/seeded — see auth_flow_integration_test.go's doc
// comment for the shared -p 1 note. This exercises System 6's document
// routes through the fully wired application, driven by real HTTP
// multipart requests exactly as a browser/client would send them —
// complementing internal/service's DocumentService-level tests (which
// exercise the business logic directly, including hash correctness and
// orphan cleanup that are awkward to assert on through raw HTTP).
package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/app"
)

type documentEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		ID           string `json:"id"`
		CaseID       string `json:"case_id"`
		DocumentType string `json:"document_type"`
		Filename     string `json:"filename"`
		MimeType     string `json:"mime_type"`
		FileSize     int64  `json:"file_size"`
		Sha256Hash   string `json:"sha256_hash"`
		Status       string `json:"status"`
		UploadedBy   string `json:"uploaded_by"`
	} `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// buildMultipartUpload builds a real multipart/form-data body with
// document_type and description written BEFORE file — the documented
// field-order contract internal/handlers/document/upload.go's streaming
// parser depends on.
func buildMultipartUpload(t *testing.T, documentType, description, filename string, content []byte) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	require.NoError(t, w.WriteField("document_type", documentType))
	if description != "" {
		require.NoError(t, w.WriteField("description", description))
	}
	fw, err := w.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = fw.Write(content)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return &buf, w.FormDataContentType()
}

func doUpload(t *testing.T, router http.Handler, caseID, bearer, documentType, description, filename string, content []byte, extraHeaders map[string]string) (*httptest.ResponseRecorder, documentEnvelope) {
	t.Helper()
	body, contentType := buildMultipartUpload(t, documentType, description, filename, content)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cases/"+caseID+"/documents", body)
	req.Header.Set("Content-Type", contentType)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var env documentEnvelope
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	}
	return rec, env
}

type errorEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// decodeErrorEnvelope extracts just the stable error{code,message} fields
// from a response body, deliberately ignoring request_id (which is
// legitimately unique per request and must not affect an "identical
// response" comparison).
func decodeErrorEnvelope(t *testing.T, rec *httptest.ResponseRecorder) errorEnvelope {
	t.Helper()
	var env struct {
		Error errorEnvelope `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	return env.Error
}

func doDownload(t *testing.T, router http.Handler, documentID, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/documents/"+documentID+"/download", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestDocumentFlow_EndToEnd(t *testing.T) {
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
	policeEmail := "doc-http-police-" + suffix + "@example.com"
	lawyerEmail := "doc-http-lawyer-" + suffix + "@example.com"

	seedCaseTestUser(t, migrator, policeEmail, "POLICE", password)
	seedCaseTestUser(t, migrator, lawyerEmail, "LAWYER", password)
	policeToken := loginAs(t, router, policeEmail, password)
	lawyerToken := loginAs(t, router, lawyerEmail, password)

	// ---- Create a case as POLICE via the real API (System 5) ----
	rec, caseResp := doJSONWithHeaders(t, router, http.MethodPost, "/api/v1/cases", map[string]any{
		"case_number": "DOC-HTTP-" + suffix, "title": "Document flow case",
	}, policeToken, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	caseID := caseResp.Data.ID
	require.NotEmpty(t, caseID)

	content := []byte("evidence file content for the HTTP flow test")

	// ---- 1. Unauthenticated upload rejected ----
	rec, _ = doUpload(t, router, caseID, "", "OTHER", "", "evidence.txt", content, nil)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// ---- 2. LAWYER cannot upload (RBAC: no document:upload permission) ----
	rec, lawyerUploadResp := doUpload(t, router, caseID, lawyerToken, "OTHER", "", "evidence.txt", content, nil)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.NotNil(t, lawyerUploadResp.Error)

	// ---- 3. X-User-ID / X-Role header spoofing has no effect on upload ----
	rec, spoofResp := doUpload(t, router, caseID, lawyerToken, "OTHER", "", "evidence.txt", content, map[string]string{
		"X-User-ID": uuid.New().String(),
		"X-Role":    "ADMIN",
	})
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "FORBIDDEN", spoofResp.Error.Code)

	// ---- 4. POLICE uploads successfully; path-traversal filename is
	// sanitized; the client-declared Content-Type on the file part
	// ("text/plain" from CreateFormFile... actually CreateFormFile always
	// sets application/octet-stream) is not trusted — the server detects
	// its own MIME type from content. ----
	rec, uploadResp := doUpload(t, router, caseID, policeToken, "WITNESS_STATEMENT", "A witness statement", "../../etc/passwd", content, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.True(t, uploadResp.Success)
	documentID := uploadResp.Data.ID
	require.NotEmpty(t, documentID)
	require.Equal(t, "passwd", uploadResp.Data.Filename, "path traversal in the client-supplied filename must be stripped")
	require.Equal(t, "WITNESS_STATEMENT", uploadResp.Data.DocumentType)
	require.Equal(t, caseID, uploadResp.Data.CaseID)
	require.Equal(t, int64(len(content)), uploadResp.Data.FileSize)
	require.Len(t, uploadResp.Data.Sha256Hash, 64, "hash must be 64 lowercase hex characters")

	// ---- 5. Invalid document_type rejected with 400 ----
	rec, _ = doUpload(t, router, caseID, policeToken, "NOT_A_TYPE", "", "x.txt", []byte("x"), nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// ---- 6. Duplicate/second upload with a distinct file also succeeds
	// (no artificial one-document-per-case limit). ----
	rec, secondResp := doUpload(t, router, caseID, policeToken, "OTHER", "", "second.txt", []byte("second file"), nil)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.NotEqual(t, documentID, secondResp.Data.ID)

	// ---- 7. Authorized download streams the exact original bytes, with
	// a safe Content-Disposition and no MinIO/internal details leaked. ----
	downloadRec := doDownload(t, router, documentID, policeToken)
	require.Equal(t, http.StatusOK, downloadRec.Code)
	require.Equal(t, content, downloadRec.Body.Bytes())
	require.Contains(t, downloadRec.Header().Get("Content-Disposition"), "attachment")
	require.Contains(t, downloadRec.Header().Get("Content-Disposition"), "passwd")
	require.Equal(t, "nosniff", downloadRec.Header().Get("X-Content-Type-Options"))
	require.NotContains(t, downloadRec.Body.String(), "minio", "no storage backend detail may leak into the response")

	// ---- 8. IDOR: an unrelated LAWYER cannot download this document ----
	unrelatedRec := doDownload(t, router, documentID, lawyerToken)
	require.Equal(t, http.StatusForbidden, unrelatedRec.Code)
	unrelatedErr := decodeErrorEnvelope(t, unrelatedRec)

	// ---- 9. IDOR: a guessed, syntactically valid but nonexistent document
	// ID produces the IDENTICAL response as step 8 (same status, same
	// code/message — request_id legitimately differs per request, so
	// compare the meaningful fields rather than the raw JSON body). ----
	guessedRec := doDownload(t, router, uuid.New().String(), lawyerToken)
	require.Equal(t, http.StatusForbidden, guessedRec.Code)
	guessedErr := decodeErrorEnvelope(t, guessedRec)
	require.Equal(t, unrelatedErr.Code, guessedErr.Code)
	require.Equal(t, unrelatedErr.Message, guessedErr.Message)

	// ---- 10. A malformed document UUID is denied identically, never a
	// distinguishable 400/404. ----
	malformedRec := doDownload(t, router, "not-a-uuid", lawyerToken)
	require.Equal(t, http.StatusForbidden, malformedRec.Code)
	malformedErr := decodeErrorEnvelope(t, malformedRec)
	require.Equal(t, unrelatedErr.Code, malformedErr.Code)
	require.Equal(t, unrelatedErr.Message, malformedErr.Message)

	// ---- 11. Oversized upload rejected with 413, via a second app
	// instance configured with a tiny MAX_UPLOAD_SIZE. ----
	t.Setenv("MAX_UPLOAD_SIZE", "8")
	smallApp, err := app.New(ctx)
	require.NoError(t, err)
	defer smallApp.Close()
	smallRouter := NewRouter(smallApp)

	rec, _ = doUpload(t, smallRouter, caseID, policeToken, "OTHER", "", "big.bin", bytes.Repeat([]byte("a"), 1024), nil)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
}
