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
// real requests, then exercises GET /audit and the full System 11
// asynchronous verification flow (POST /audit/verify-chain -> 202 ->
// poll GET .../verify-chain/:id -> VERIFIED/INTEGRITY_FAILURE, plus SSE)
// against a REAL embedded Asynq worker — see newTestAuditWorker below.
// Complements internal/service's AuditService-level tests (which
// exercise the RBAC/RLS/verification matrix directly, without a real
// Asynq round trip).
package httpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/app"
	"evidentia/backend/internal/jobs"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

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

type startVerificationEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		VerificationID string    `json:"verification_id"`
		JobID          string    `json:"job_id"`
		Status         string    `json:"status"`
		CreatedAt      time.Time `json:"created_at"`
	} `json:"data"`
	Error *errorEnvelope `json:"error"`
}

type verificationDetailEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		VerificationID  string   `json:"verification_id"`
		JobID           string   `json:"job_id"`
		Status          string   `json:"status"`
		EntriesChecked  int64    `json:"entries_checked"`
		TotalEntries    *int64   `json:"total_entries"`
		ProgressPercent *float64 `json:"progress_percent"`
		FailedEntryID   *string  `json:"failed_entry_id"`
		FailedSeq       *int64   `json:"failed_seq"`
		FailureType     string   `json:"failure_type"`
		FailureReason   string   `json:"failure_reason"`
	} `json:"data"`
	Error *errorEnvelope `json:"error"`
}

type verificationHistoryEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		Verifications []struct {
			VerificationID string `json:"verification_id"`
			Status         string `json:"status"`
		} `json:"verifications"`
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	} `json:"data"`
	Error *errorEnvelope `json:"error"`
}

type integritySummaryEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		TotalEntries     int64  `json:"total_entries"`
		ChainHeadSeq     *int64 `json:"chain_head_seq"`
		ChainHeadHash    string `json:"chain_head_hash"`
		LastVerification *struct {
			Status string `json:"status"`
		} `json:"last_verification"`
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

func doStartVerification(t *testing.T, router http.Handler, bearer string) (*httptest.ResponseRecorder, startVerificationEnvelope) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/audit/verify-chain", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var env startVerificationEnvelope
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	}
	return rec, env
}

func doVerificationStatus(t *testing.T, router http.Handler, bearer, verificationID string) (*httptest.ResponseRecorder, verificationDetailEnvelope) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify-chain/"+verificationID, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var env verificationDetailEnvelope
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	}
	return rec, env
}

func doVerificationHistory(t *testing.T, router http.Handler, bearer string) (*httptest.ResponseRecorder, verificationHistoryEnvelope) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/verifications", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var env verificationHistoryEnvelope
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	}
	return rec, env
}

func doIntegritySummary(t *testing.T, router http.Handler, bearer string) (*httptest.ResponseRecorder, integritySummaryEnvelope) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/integrity", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var env integritySummaryEnvelope
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	}
	return rec, env
}

// pollVerificationUntilTerminal polls GET /audit/verify-chain/:id until
// status leaves QUEUED/RUNNING or the deadline elapses — this test's
// stand-in for "wait for the SSE stream to say it's done" when a plain
// REST-only assertion is simpler, exercising the exact same
// AuditService.GetVerification path the SSE handler's own initial
// snapshot uses.
func pollVerificationUntilTerminal(t *testing.T, router http.Handler, bearer, verificationID string) verificationDetailEnvelope {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		rec, env := doVerificationStatus(t, router, bearer, verificationID)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		if env.Data.Status != "QUEUED" && env.Data.Status != "RUNNING" {
			return env
		}
		if time.Now().After(deadline) {
			t.Fatalf("verification %s did not reach a terminal state within the deadline (last status: %s)", verificationID, env.Data.Status)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// newTestAuditWorker starts a REAL Asynq worker (server + mux) against
// the same AuditService the router itself uses, so POST /audit/
// verify-chain's enqueued task is actually picked up and processed —
// exactly the production architecture (see cmd/server/main.go), not a
// direct RunVerification call bypassing the queue (that variant is
// covered by internal/service's own integration tests). Returns a
// shutdown func the caller must defer.
func newTestAuditWorker(t *testing.T, application *app.App) func() {
	t.Helper()
	redisOpt := asynq.RedisClientOpt{Addr: envOr("REDIS_ADDR", "localhost:6379")}
	errorHandler := jobs.NewAuditVerificationErrorHandler(application.AuditService, application.Logger)
	server := jobs.NewServer(redisOpt, errorHandler, application.Logger)
	mux := jobs.NewMux(application.Logger, jobs.NewAuditVerificationHandler(application.AuditService))

	go func() {
		_ = server.Run(mux)
	}()

	return server.Shutdown
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

	shutdownWorker := newTestAuditWorker(t, application)
	defer shutdownWorker()

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
	rec, _ = doStartVerification(t, router, forensicsToken)
	require.Equal(t, http.StatusForbidden, rec.Code)
	rec, _ = doStartVerification(t, router, policeToken)
	require.Equal(t, http.StatusForbidden, rec.Code)

	// 7. ADMIN starts verification: 202 Accepted, QUEUED (or already
	// RUNNING if the worker picked it up between the INSERT and this
	// response — either is valid), never a synchronous final result.
	rec, startResp := doStartVerification(t, router, adminToken)
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	require.NotEmpty(t, startResp.Data.VerificationID)
	require.Contains(t, []string{"QUEUED", "RUNNING"}, startResp.Data.Status)
	verificationID := startResp.Data.VerificationID
	// System 12: job_id is a traceable, deterministic identifier for the
	// underlying Asynq task — see jobs.AuditVerifyChainJobID.
	require.Equal(t, "audit:verify_chain:"+verificationID, startResp.Data.JobID)

	// 8. IDOR: POLICE/FORENSICS cannot inspect this verification either.
	rec, _ = doVerificationStatus(t, router, policeToken, verificationID)
	require.Equal(t, http.StatusForbidden, rec.Code)

	// 9. The embedded worker (started above) actually processes the job:
	// polling the REST status endpoint (never a fake/simulated frontend
	// progress) reaches a terminal VERIFIED result.
	final := pollVerificationUntilTerminal(t, router, adminToken, verificationID)
	require.Equal(t, "VERIFIED", final.Data.Status, "this database's chain, however large from other tests/fixtures, must still be internally consistent")
	require.Equal(t, startResp.Data.JobID, final.Data.JobID, "job_id must be stable across the entire run, not just the initial 202 response")
	require.Greater(t, *final.Data.TotalEntries, int64(0))
	require.NotNil(t, final.Data.ProgressPercent)
	require.InDelta(t, 100.0, *final.Data.ProgressPercent, 0.01)

	// 10. History and integrity-summary endpoints reflect the completed run.
	rec, history := doVerificationHistory(t, router, adminToken)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	foundRun := false
	for _, v := range history.Data.Verifications {
		if v.VerificationID == verificationID {
			foundRun = true
			require.Equal(t, "VERIFIED", v.Status)
		}
	}
	require.True(t, foundRun, "the completed verification must appear in GET /audit/verifications")

	rec, summary := doIntegritySummary(t, router, adminToken)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, summary.Data.LastVerification)
	require.Equal(t, "VERIFIED", summary.Data.LastVerification.Status)
	require.NotEmpty(t, summary.Data.ChainHeadHash)

	// 11. Tamper, then verify again: INTEGRITY_FAILURE, with safe failure
	// detail — the mandatory "tampering is detected end-to-end" scenario.
	// Captures the genesis entry's real action first and restores it via
	// defer (so it runs even if a later assertion fails this test early):
	// audit_log here is a SHARED table other integration tests in this
	// suite (and manual/Docker verification) also read against, so leaving
	// it permanently tampered would fail every subsequent verification run
	// — never acceptable, even in a test/dev database (master prompt's
	// "restore only the test environment" applies here too, not only to a
	// hypothetical production one).
	var originalGenesisAction string
	require.NoError(t, migrator.QueryRow(ctx, `SELECT action FROM audit_log WHERE seq = (SELECT MIN(seq) FROM audit_log)`).Scan(&originalGenesisAction))
	_, err = migrator.Exec(ctx, `UPDATE audit_log SET action = 'HTTP_FLOW_TAMPERED' WHERE seq = (SELECT MIN(seq) FROM audit_log)`)
	require.NoError(t, err)
	defer func() {
		// context.Background(), not ctx: this must still run even if ctx's
		// own deadline has already passed by the time this defer fires.
		_, _ = migrator.Exec(context.Background(), `UPDATE audit_log SET action = $1 WHERE seq = (SELECT MIN(seq) FROM audit_log)`, originalGenesisAction)
	}()

	rec, tamperStart := doStartVerification(t, router, adminToken)
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	tamperFinal := pollVerificationUntilTerminal(t, router, adminToken, tamperStart.Data.VerificationID)
	require.Equal(t, "INTEGRITY_FAILURE", tamperFinal.Data.Status)
	require.NotEmpty(t, tamperFinal.Data.FailureType)
	require.NotContains(t, rec.Body.String(), "SELECT", "no SQL text may leak into the verification response")
}

// TestAuditFlow_SSE exercises the real Server-Sent-Events stream over a
// genuine network connection (httptest.Server, not httptest.
// ResponseRecorder, which cannot stream) — connects, reads frames as they
// arrive, and confirms a terminal event closes the stream.
func TestAuditFlow_SSE(t *testing.T) {
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

	shutdownWorker := newTestAuditWorker(t, application)
	defer shutdownWorker()

	server := httptest.NewServer(router)
	defer server.Close()

	migrator, err := pgxpool.New(ctx, "postgres://evidentia:changeme_example@localhost:5432/evidentia?sslmode=disable")
	require.NoError(t, err)
	defer migrator.Close()

	const password = "correct horse battery staple"
	suffix := uuid.New().String()[:8]
	adminEmail := "audit-sse-admin-" + suffix + "@example.com"
	policeEmail := "audit-sse-police-" + suffix + "@example.com"
	seedCaseTestUser(t, migrator, adminEmail, "ADMIN", password)
	seedCaseTestUser(t, migrator, policeEmail, "POLICE", password)

	loginReq, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/auth/login", strings.NewReader(`{"email":"`+adminEmail+`","password":"`+password+`"}`))
	require.NoError(t, err)
	loginReq.Header.Set("Content-Type", "application/json")
	loginResp, err := http.DefaultClient.Do(loginReq)
	require.NoError(t, err)
	defer loginResp.Body.Close()
	var loginEnv struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(loginResp.Body).Decode(&loginEnv))
	adminToken := loginEnv.Data.AccessToken
	require.NotEmpty(t, adminToken)

	policeReq, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/auth/login", strings.NewReader(`{"email":"`+policeEmail+`","password":"`+password+`"}`))
	require.NoError(t, err)
	policeReq.Header.Set("Content-Type", "application/json")
	policeResp, err := http.DefaultClient.Do(policeReq)
	require.NoError(t, err)
	defer policeResp.Body.Close()
	var policeEnv struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(policeResp.Body).Decode(&policeEnv))
	policeToken := policeEnv.Data.AccessToken
	require.NotEmpty(t, policeToken)

	startReq, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/audit/verify-chain", nil)
	require.NoError(t, err)
	startReq.Header.Set("Authorization", "Bearer "+adminToken)
	startResp, err := http.DefaultClient.Do(startReq)
	require.NoError(t, err)
	defer startResp.Body.Close()
	require.Equal(t, http.StatusAccepted, startResp.StatusCode)
	var startEnv startVerificationEnvelope
	require.NoError(t, json.NewDecoder(startResp.Body).Decode(&startEnv))
	verificationID := startEnv.Data.VerificationID

	// Unauthorized SSE connection: POLICE holds no audit:verify.
	unauthorizedReq, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/audit/verify-chain/"+verificationID+"/events", nil)
	require.NoError(t, err)
	unauthorizedReq.Header.Set("Authorization", "Bearer "+policeToken)
	unauthorizedResp, err := http.DefaultClient.Do(unauthorizedReq)
	require.NoError(t, err)
	defer unauthorizedResp.Body.Close()
	require.Equal(t, http.StatusForbidden, unauthorizedResp.StatusCode, "SSE must be authenticated/authorized exactly like the REST endpoint — verification_id alone proves nothing")

	// Authorized SSE connection: read frames until a terminal event.
	sseCtx, sseCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer sseCancel()
	sseReq, err := http.NewRequestWithContext(sseCtx, http.MethodGet, server.URL+"/api/v1/audit/verify-chain/"+verificationID+"/events", nil)
	require.NoError(t, err)
	sseReq.Header.Set("Authorization", "Bearer "+adminToken)
	sseResp, err := http.DefaultClient.Do(sseReq)
	require.NoError(t, err)
	defer sseResp.Body.Close()
	require.Equal(t, http.StatusOK, sseResp.StatusCode)
	require.Contains(t, sseResp.Header.Get("Content-Type"), "text/event-stream")

	terminalTypes := map[string]bool{
		"verification_completed":         true,
		"verification_integrity_failure": true,
		"verification_failed":            true,
	}

	scanner := bufio.NewScanner(sseResp.Body)
	var sawEvent, sawTerminal bool
	var lastEventType string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			lastEventType = strings.TrimPrefix(line, "event: ")
			sawEvent = true
		}
		if strings.HasPrefix(line, "data: ") && terminalTypes[lastEventType] {
			sawTerminal = true
			break
		}
	}
	require.True(t, sawEvent, "the SSE stream must send at least the initial current-state event")
	require.True(t, sawTerminal, "the SSE stream must close with a terminal event once verification completes")
}
