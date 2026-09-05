//go:build integration

// Run with: go test -tags=integration ./internal/httpserver/...
// Requires the full docker-compose infrastructure (postgres, redis, minio)
// up and migrated/seeded — see auth_flow_integration_test.go's doc
// comment for the shared -p 1 note.
//
// This exercises System 5's case routes through the fully wired
// application (config -> app container -> router), driven by real HTTP
// requests exactly as a client would experience them — complementing
// internal/service's CaseService-level tests (which exercise the business
// logic directly) and backend/tests' RLS-level tests (which exercise raw
// row visibility).
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
	"evidentia/backend/internal/auth"
)

type caseEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		ID         string `json:"id"`
		CaseNumber string `json:"case_number"`
		Status     string `json:"status"`
		CreatedBy  string `json:"created_by"`
		Cases      []struct {
			ID         string `json:"id"`
			CaseNumber string `json:"case_number"`
		} `json:"cases"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	} `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func doJSONWithHeaders(t *testing.T, router http.Handler, method, path string, body any, bearer string, headers map[string]string) (*httptest.ResponseRecorder, caseEnvelope) {
	t.Helper()
	rec, raw := doJSONRaw(t, router, method, path, body, bearer, headers)
	var env caseEnvelope
	if len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, &env))
	}
	return rec, env
}

func doJSONRaw(t *testing.T, router http.Handler, method, path string, body any, bearer string, headers map[string]string) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	var buf []byte
	if body != nil {
		var err error
		buf, err = json.Marshal(body)
		require.NoError(t, err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec, rec.Body.Bytes()
}

// loginAs logs email/password in through the real /auth/login route and
// returns the resulting access token — every case-route test below
// authenticates exactly as a real client would, never by hand-crafting a
// JWT or attaching auth.AuthenticatedUser directly.
func loginAs(t *testing.T, router http.Handler, email, password string) string {
	t.Helper()
	rec, env := doJSON(t, router, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": email, "password": password,
	}, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, env.Success)
	return env.Data.AccessToken
}

// seedCaseTestUser inserts a fresh user (email must be unique per test
// run — see TestCaseFlow_EndToEnd's suffix — since unlike
// auth_flow_integration_test.go's users, these go on to own cases via
// cases.created_by, which is ON DELETE RESTRICT: a stale row from a
// previous run cannot simply be deleted and re-inserted the way a
// dependency-free user can).
func seedCaseTestUser(t *testing.T, migrator *pgxpool.Pool, email, roleName, password string) {
	t.Helper()
	ctx := context.Background()
	hash, err := auth.HashPassword(password, 4)
	require.NoError(t, err)

	var userID uuid.UUID
	require.NoError(t, migrator.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, first_name, last_name, status) VALUES ($1, $2, 'Case', 'Test', 'active') RETURNING id`,
		email, hash,
	).Scan(&userID))

	if roleName != "" {
		_, err := migrator.Exec(ctx, `
			INSERT INTO user_roles (user_id, role_id)
			SELECT $1, id FROM roles WHERE name = $2`, userID, roleName)
		require.NoError(t, err)
	}
}

func TestCaseFlow_EndToEnd(t *testing.T) {
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

	// Every user email and case_number below is suffixed with a fresh UUID
	// so repeated runs of this test against the same, never-truncated-
	// between-runs database (unlike the RLS suite's helpers_test.go, which
	// does truncate) never collide with a previous run's rows — neither
	// users-that-own-cases nor cases themselves are ever hard-deleted (see
	// the migration), so cleanup-by-truncation/delete isn't an option here
	// the way it is for backend/tests or auth_flow_integration_test.go.
	suffix := uuid.New().String()[:8]
	mainCaseNumber := "HTTP-CASE-" + suffix
	policeEmail := "http-police-" + suffix + "@example.com"
	lawyerEmail := "http-lawyer-" + suffix + "@example.com"
	adminEmail := "http-admin-" + suffix + "@example.com"

	const password = "correct horse battery staple"
	seedCaseTestUser(t, migrator, policeEmail, "POLICE", password)
	seedCaseTestUser(t, migrator, lawyerEmail, "LAWYER", password)
	seedCaseTestUser(t, migrator, adminEmail, "ADMIN", password)

	policeToken := loginAs(t, router, policeEmail, password)
	lawyerToken := loginAs(t, router, lawyerEmail, password)
	adminToken := loginAs(t, router, adminEmail, password)

	// ---- 1. Unauthenticated create is rejected ----
	rec, _ := doJSONWithHeaders(t, router, http.MethodPost, "/api/v1/cases", map[string]any{
		"case_number": "HTTP-NOAUTH-" + suffix, "title": "No auth",
	}, "", nil)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// ---- 2. LAWYER cannot create a case (RBAC) ----
	rec, lawyerCreateResp := doJSONWithHeaders(t, router, http.MethodPost, "/api/v1/cases", map[string]any{
		"case_number": "HTTP-LAWYER-DENIED-" + suffix, "title": "Should be denied",
	}, lawyerToken, nil)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.NotNil(t, lawyerCreateResp.Error)
	require.Equal(t, "FORBIDDEN", lawyerCreateResp.Error.Code)

	// ---- 3. POLICE can create a case; created_by is server-controlled ----
	// even though the body tries to claim a different creator via an
	// unrecognized field — createCaseRequest has no such field to bind
	// into, so this is a structural, not just behavioral, guarantee.
	rec, createResp := doJSONWithHeaders(t, router, http.MethodPost, "/api/v1/cases", map[string]any{
		"case_number": mainCaseNumber, "title": "HTTP case", "created_by": uuid.New().String(),
	}, policeToken, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.True(t, createResp.Success)
	caseID := createResp.Data.ID
	require.NotEmpty(t, caseID)

	// ---- 4. X-User-ID / X-Role header spoofing has no effect ----
	// A LAWYER presenting these headers still gets denied exactly as in
	// step 2 — identity/role come only from the validated JWT plus the
	// server-side database lookup, never from client-supplied headers.
	rec, spoofResp := doJSONWithHeaders(t, router, http.MethodPost, "/api/v1/cases", map[string]any{
		"case_number": "HTTP-SPOOF-" + suffix, "title": "Spoofed",
	}, lawyerToken, map[string]string{
		"X-User-ID": uuid.New().String(),
		"X-Role":    "ADMIN",
	})
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "FORBIDDEN", spoofResp.Error.Code)

	// ---- 5. GET /cases/:id — creator can read their own case ----
	rec, getResp := doJSONWithHeaders(t, router, http.MethodGet, "/api/v1/cases/"+caseID, nil, policeToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, mainCaseNumber, getResp.Data.CaseNumber)

	// ---- 6. IDOR: an unrelated LAWYER cannot read this case ----
	rec, unrelatedResp := doJSONWithHeaders(t, router, http.MethodGet, "/api/v1/cases/"+caseID, nil, lawyerToken, nil)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "FORBIDDEN", unrelatedResp.Error.Code)

	// ---- 7. IDOR: a guessed, syntactically valid but nonexistent case ID
	// produces the IDENTICAL response as step 6 (same status, same body
	// shape) — a client cannot distinguish "doesn't exist" from "exists
	// but isn't yours". ----
	rec, guessedResp := doJSONWithHeaders(t, router, http.MethodGet, "/api/v1/cases/"+uuid.New().String(), nil, lawyerToken, nil)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, unrelatedResp.Error.Code, guessedResp.Error.Code)
	require.Equal(t, unrelatedResp.Error.Message, guessedResp.Error.Message)

	// ---- 8. A malformed UUID path parameter is denied identically, not a
	// distinguishable 400. ----
	rec, malformedResp := doJSONWithHeaders(t, router, http.MethodGet, "/api/v1/cases/not-a-uuid", nil, lawyerToken, nil)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, unrelatedResp.Error.Message, malformedResp.Error.Message)

	// ---- 9. ADMIN can read any case ----
	rec, adminGetResp := doJSONWithHeaders(t, router, http.MethodGet, "/api/v1/cases/"+caseID, nil, adminToken, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, mainCaseNumber, adminGetResp.Data.CaseNumber)

	// ---- 10. PUT /cases/:id — owner can update ----
	rec, updateResp := doJSONWithHeaders(t, router, http.MethodPut, "/api/v1/cases/"+caseID, map[string]any{
		"title": "Updated via HTTP", "status": "UNDER_INVESTIGATION",
	}, policeToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "UNDER_INVESTIGATION", updateResp.Data.Status)

	// ---- 11. An unrelated LAWYER cannot update this case ----
	rec, _ = doJSONWithHeaders(t, router, http.MethodPut, "/api/v1/cases/"+caseID, map[string]any{
		"title": "Hijacked", "status": "CLOSED",
	}, lawyerToken, nil)
	require.Equal(t, http.StatusForbidden, rec.Code)

	// ---- 12. An invalid status transition is rejected with 400 ----
	rec, _ = doJSONWithHeaders(t, router, http.MethodPut, "/api/v1/cases/"+caseID, map[string]any{
		"title": "Updated via HTTP", "status": "NOT_A_STATUS",
	}, policeToken, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// ---- 13. GET /cases (list) — creator sees their case, pagination
	// metadata is present. ----
	rec, listResp := doJSONWithHeaders(t, router, http.MethodGet, "/api/v1/cases?page=1&page_size=10", nil, policeToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.GreaterOrEqual(t, listResp.Data.Meta.Total, int64(1))
	found := false
	for _, c := range listResp.Data.Cases {
		if c.CaseNumber == mainCaseNumber {
			found = true
		}
	}
	require.True(t, found, "creator's own case must appear in their case list")

	// ---- 14. Duplicate case_number is rejected with 409 ----
	rec, _ = doJSONWithHeaders(t, router, http.MethodPost, "/api/v1/cases", map[string]any{
		"case_number": mainCaseNumber, "title": "Duplicate",
	}, policeToken, nil)
	require.Equal(t, http.StatusConflict, rec.Code)
}
