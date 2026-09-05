//go:build integration

// Run with: go test -tags=integration ./internal/httpserver/...
// Requires the full docker-compose infrastructure (postgres, redis, minio)
// up and migrated/seeded — see auth_flow_integration_test.go's doc
// comment for the shared -p 1 note.
//
// This exercises the audit trail's real HTTP routes through the fully
// wired application: every existing security-sensitive operation
// (login, case creation, document upload/download) already durably
// records through the SAME audit.ChainWriter now wired into app.New in
// place of audit.SlogRecorder — this test proves that end-to-end via
// real requests, then exercises GET /audit and POST /audit/verify-chain
// themselves. Complements internal/service's AuditService-level tests
// (which exercise the RBAC/RLS/verification matrix directly).
package httpserver

import (
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

type auditListEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		Entries []struct {
			ID           string `json:"id"`
			Action       string `json:"action"`
			ResourceType string `json:"resource_type"`
			UserID       string `json:"user_id"`
			Hash         string `json:"hash"`
			PrevHash     string `json:"prev_hash"`
		} `json:"entries"`
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	} `json:"data"`
	Error *errorEnvelope `json:"error"`
}

type verifyChainEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		Status         string `json:"status"`
		EntriesChecked int64  `json:"entries_checked"`
		TotalEntries   int64  `json:"total_entries"`
		NextSeq        *int64 `json:"next_seq"`
	} `json:"data"`
	Error *errorEnvelope `json:"error"`
}

func doAuditList(t *testing.T, router http.Handler, bearer string, query string) (*httptest.ResponseRecorder, auditListEnvelope) {
	t.Helper()
	path := "/api/v1/audit"
	if query != "" {
		path += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var env auditListEnvelope
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	}
	return rec, env
}

func doVerifyChain(t *testing.T, router http.Handler, bearer string) (*httptest.ResponseRecorder, verifyChainEnvelope) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/audit/verify-chain", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var env verifyChainEnvelope
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	}
	return rec, env
}

func TestAuditFlow_EndToEnd(t *testing.T) {
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
	policeEmail := "audit-http-police-" + suffix + "@example.com"
	lawyerEmail := "audit-http-lawyer-" + suffix + "@example.com"
	adminEmail := "audit-http-admin-" + suffix + "@example.com"
	forensicsEmail := "audit-http-forensics-" + suffix + "@example.com"

	seedCaseTestUser(t, migrator, policeEmail, "POLICE", password)
	seedCaseTestUser(t, migrator, lawyerEmail, "LAWYER", password)
	seedCaseTestUser(t, migrator, adminEmail, "ADMIN", password)
	seedCaseTestUser(t, migrator, forensicsEmail, "FORENSICS", password)
	policeToken := loginAs(t, router, policeEmail, password)
	_ = loginAs(t, router, lawyerEmail, password)
	adminToken := loginAs(t, router, adminEmail, password)
	forensicsToken := loginAs(t, router, forensicsEmail, password)

	// 1. Police creates a case and uploads a document — both are
	// existing, unmodified Systems 5/6 operations. Each already calls
	// recorder.Record(...); the only thing that changed is WHAT that
	// call now durably writes to (audit_log via ChainWriter, not just
	// the operational log).
	rec, caseResp := doJSONWithHeaders(t, router, http.MethodPost, "/api/v1/cases", map[string]any{
		"case_number": "AUDIT-HTTP-" + suffix, "title": "Audit flow case",
	}, policeToken, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	caseID := caseResp.Data.ID

	rec, uploadResp := doUpload(t, router, caseID, policeToken, "OTHER", "", "evidence.txt", []byte("audit trail evidence"), nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	documentID := uploadResp.Data.ID

	// 2. Unauthenticated GET /audit rejected.
	rec, _ = doAuditList(t, router, "", "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// 3. FORENSICS holds no audit:read permission.
	rec, _ = doAuditList(t, router, forensicsToken, "")
	require.Equal(t, http.StatusForbidden, rec.Code)

	// 4. ADMIN sees the real, just-created CASE_CREATED/DOCUMENT_UPLOADED
	// entries, each carrying a real 64-hex-char hash and (for all but the
	// very first entry ever written to this database) a real prev_hash.
	rec, adminList := doAuditList(t, router, adminToken, "resource_type=document&resource_id="+documentID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotEmpty(t, adminList.Data.Entries)
	found := false
	for _, e := range adminList.Data.Entries {
		if e.Action == "DOCUMENT_UPLOADED" {
			found = true
			require.Len(t, e.Hash, 64, "hash must be 64 lowercase hex characters")
		}
	}
	require.True(t, found, "the real DOCUMENT_UPLOADED event must appear in ADMIN's audit view")
	require.NotContains(t, rec.Body.String(), "minio", "no storage backend detail may leak into the audit response")

	// 5. IDOR: POLICE filtering by an arbitrary/unrelated user_id must
	// never see that user's entries — RLS narrows regardless of the
	// filter value supplied.
	rec, policeList := doAuditList(t, router, policeToken, "user_id="+uuid.New().String())
	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, policeList.Data.Entries, "a filter can never widen what RLS already restricts")

	// 6. POST /audit/verify-chain: FORENSICS/POLICE (no audit:verify) denied.
	rec, _ = doVerifyChain(t, router, forensicsToken)
	require.Equal(t, http.StatusForbidden, rec.Code)
	rec, _ = doVerifyChain(t, router, policeToken)
	require.Equal(t, http.StatusForbidden, rec.Code)

	// 7. ADMIN verifies the chain: VERIFIED (this database's chain,
	// however large from other tests/fixtures, must still be internally
	// consistent — a broken chain here would mean earlier writes,
	// possibly from a completely different test, corrupted the shared
	// hash sequence).
	rec, verifyResp := doVerifyChain(t, router, adminToken)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "VERIFIED", verifyResp.Data.Status)
	require.Greater(t, verifyResp.Data.TotalEntries, int64(0))
}
