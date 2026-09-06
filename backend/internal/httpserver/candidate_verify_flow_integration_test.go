//go:build integration

package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/app"
)

type candidateVerifyEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		Status            string  `json:"status"`
		DocumentID        string  `json:"document_id"`
		Version           int32   `json:"version"`
		ExpectedHash      string  `json:"expected_hash"`
		ActualHash        string  `json:"actual_hash"`
		BlockchainHash    *string `json:"blockchain_hash,omitempty"`
		DatabaseMatch     bool    `json:"database_match"`
		BlockchainMatch   bool    `json:"blockchain_match"`
		BlockchainStatus  string  `json:"blockchain_status"`
		FileHash          string  `json:"file_hash"`
		StoredHash        string  `json:"stored_hash"`
		BlockchainEnabled bool    `json:"blockchain_enabled"`
		VerifiedAt        string  `json:"verified_at"`
		Source            string  `json:"source"`
		Details           string  `json:"details"`
	} `json:"data"`
	Error *errorEnvelope `json:"error"`
}

func doVerifyCandidate(
	t *testing.T,
	router http.Handler,
	documentID, bearer string,
	candidateContent []byte,
	candidateFilename string,
	sourceParam string,
) (*httptest.ResponseRecorder, candidateVerifyEnvelope) {
	t.Helper()

	var (
		req *http.Request
		url = "/api/v1/documents/" + documentID + "/verify-candidate"
	)

	if sourceParam != "" {
		url += "?source=" + sourceParam
	}

	if len(candidateContent) > 0 {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", candidateFilename)
		require.NoError(t, err)
		_, err = part.Write(candidateContent)
		require.NoError(t, err)
		require.NoError(t, writer.Close())

		req = httptest.NewRequest(http.MethodPost, url, &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
	} else {
		req = httptest.NewRequest(http.MethodPost, url, nil)
	}

	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var env candidateVerifyEnvelope
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
	}
	return rec, env
}

func TestCandidateVerifyFlow_EndToEnd(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
	policeEmail := "candidate-police-" + suffix + "@example.com"
	unauthPoliceEmail := "candidate-unauth-" + suffix + "@example.com"
	lawyerEmail := "candidate-lawyer-" + suffix + "@example.com"

	seedCaseTestUser(t, migrator, policeEmail, "POLICE", password)
	seedCaseTestUser(t, migrator, unauthPoliceEmail, "POLICE", password)
	seedCaseTestUser(t, migrator, lawyerEmail, "LAWYER", password)

	policeToken := loginAs(t, router, policeEmail, password)
	unauthPoliceToken := loginAs(t, router, unauthPoliceEmail, password)
	lawyerToken := loginAs(t, router, lawyerEmail, password)

	// Create Case
	rec, caseResp := doJSONWithHeaders(t, router, http.MethodPost, "/api/v1/cases", map[string]any{
		"case_number": "CASE-DEMO-" + suffix,
		"title":       "Candidate Verification Case",
	}, policeToken, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	caseID := caseResp.Data.ID

	// Upload Evidence
	originalContent := []byte("OFFICIAL EVIDENCE — REGISTRATION HASH ORIGINAL")
	rec, uploadResp := doUpload(t, router, caseID, policeToken, "PHOTO_EVIDENCE", "Demo evidence", "evidence_original.pdf", originalContent, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	docID := uploadResp.Data.ID

	// 1. Unauthorized / Non-authenticated access (Test 9)
	t.Run("Unauthorized user returns 401/403", func(t *testing.T) {
		recNoAuth, _ := doVerifyCandidate(t, router, docID, "", originalContent, "candidate.pdf", "")
		assert.Equal(t, http.StatusUnauthorized, recNoAuth.Code)

		recUnrelated, _ := doVerifyCandidate(t, router, docID, unauthPoliceToken, originalContent, "candidate.pdf", "")
		assert.Equal(t, http.StatusForbidden, recUnrelated.Code)

		recLawyer, _ := doVerifyCandidate(t, router, docID, lawyerToken, originalContent, "candidate.pdf", "")
		assert.Equal(t, http.StatusForbidden, recLawyer.Code)
	})

	// 2. Cross-case access (Test 10)
	t.Run("Cross-case document access denied", func(t *testing.T) {
		otherUUID := uuid.New().String()
		recCross, _ := doVerifyCandidate(t, router, otherUUID, policeToken, originalContent, "candidate.pdf", "")
		assert.Equal(t, http.StatusForbidden, recCross.Code)
	})

	// 3. Stored evidence verify
	t.Run("Stored evidence returns VERIFIED", func(t *testing.T) {
		recStored, envStored := doVerifyCandidate(t, router, docID, policeToken, nil, "", "stored")
		require.Equal(t, http.StatusOK, recStored.Code)
		assert.True(t, envStored.Success)
		assert.Equal(t, "VERIFIED", envStored.Data.Status)
		assert.True(t, envStored.Data.DatabaseMatch)
		assert.Equal(t, "stored", envStored.Data.Source)
	})

	// 4. Candidate matching original evidence (Test 1)
	t.Run("Matching candidate returns VERIFIED", func(t *testing.T) {
		recMatch, envMatch := doVerifyCandidate(t, router, docID, policeToken, originalContent, "evidence_original.pdf", "")
		require.Equal(t, http.StatusOK, recMatch.Code)
		assert.True(t, envMatch.Success)
		assert.Equal(t, "VERIFIED", envMatch.Data.Status)
		assert.True(t, envMatch.Data.DatabaseMatch)
		assert.Equal(t, envMatch.Data.ExpectedHash, envMatch.Data.ActualHash)
		assert.Equal(t, "candidate", envMatch.Data.Source)
	})

	// 5. Candidate with tampering / modification (Test 2 & 3)
	t.Run("Modified candidate returns INTEGRITY_FAILURE without modifying original", func(t *testing.T) {
		tamperedContent := []byte("TAMPERED EVIDENCE — MODIFIED CONTENT DETECTED")
		recTampered, envTampered := doVerifyCandidate(t, router, docID, policeToken, tamperedContent, "evidence_modified.pdf", "")
		require.Equal(t, http.StatusOK, recTampered.Code)
		assert.True(t, envTampered.Success)
		assert.Equal(t, "INTEGRITY_FAILURE", envTampered.Data.Status)
		assert.False(t, envTampered.Data.DatabaseMatch)
		assert.False(t, envTampered.Data.BlockchainMatch)
		assert.NotEqual(t, envTampered.Data.ExpectedHash, envTampered.Data.ActualHash)
		assert.Equal(t, "candidate", envTampered.Data.Source)
		assert.Contains(t, envTampered.Data.Details, "submitted file differs")

		// Verify original record was NOT mutated
		recRecheck, envRecheck := doVerifyCandidate(t, router, docID, policeToken, nil, "", "stored")
		require.Equal(t, http.StatusOK, recRecheck.Code)
		assert.Equal(t, "VERIFIED", envRecheck.Data.Status)
		assert.True(t, envRecheck.Data.DatabaseMatch)
	})
}
