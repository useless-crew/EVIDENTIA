//go:build integration

// Run with: go test -tags=integration ./internal/httpserver/...
// Requires the full docker-compose infrastructure (postgres, redis, minio)
// up and migrated (go run ./cmd/migrate up), plus a seeded LAWYER role
// (backend/db/seed/001_reference_data.sql) or one created ad hoc below.
//
// This is master prompt §74's real, local, end-to-end auth flow — driven
// entirely through real HTTP requests against the actual router (not the
// service layer directly, which internal/service's own integration tests
// already exercise), so it validates the full handler+middleware+service
// wiring together, exactly as a real client would experience it.
//
// Add -p 1 when running alongside other packages' integration tests in one
// invocation (go test -tags=integration ./...) — this test, internal/
// service, and backend/tests all touch the shared users/auth_sessions
// tables in the same live database, and Go runs different packages' tests
// concurrently by default. See backend/tests/helpers_test.go for the full
// explanation.
package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/app"
	"evidentia/backend/internal/auth"
)

type envelope struct {
	Success bool `json:"success"`
	Data    struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	} `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func doJSON(t *testing.T, router http.Handler, method, path string, body any, bearer string) (*httptest.ResponseRecorder, envelope) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var env envelope
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	}
	return rec, env
}

func TestAuthFlow_EndToEnd(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	application, err := app.New(ctx)
	require.NoError(t, err)
	defer application.Close()

	router := NewRouter(application)

	// ---- Fixture: a fresh active user with a known password ----
	migrator, err := pgxpool.New(ctx, "postgres://evidentia:changeme_example@localhost:5432/evidentia?sslmode=disable")
	require.NoError(t, err)
	defer migrator.Close()

	const email = "e2e-flow@example.com"
	const password = "correct horse battery staple"
	hash, err := auth.HashPassword(password, 4)
	require.NoError(t, err)
	_, _ = migrator.Exec(ctx, `DELETE FROM users WHERE email = $1`, email)
	_, err = migrator.Exec(ctx,
		`INSERT INTO users (email, password_hash, first_name, last_name, status) VALUES ($1, $2, 'E2E', 'Flow', 'active')`,
		email, hash)
	require.NoError(t, err)

	// 1. Login.
	rec, loginResp := doJSON(t, router, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": email, "password": password,
	}, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, loginResp.Success)
	require.NotEmpty(t, loginResp.Data.AccessToken)
	require.NotEmpty(t, loginResp.Data.RefreshToken)
	firstAccess := loginResp.Data.AccessToken

	// 2. A protected endpoint (logout, the only one this system has)
	// accepts the access token.
	rec, _ = doJSON(t, router, http.MethodPost, "/api/v1/auth/logout", map[string]string{}, firstAccess)
	require.Equal(t, http.StatusOK, rec.Code, "logout with no refresh_token in the body is a valid no-op, still requires a valid access token")

	// 3. Refresh — issued fresh via a second login, since step 2 didn't
	// revoke anything (empty refresh_token) but demonstrates the access
	// token authenticated correctly. Login again to get a clean session
	// for the rotation/reuse checks below.
	rec, loginResp = doJSON(t, router, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": email, "password": password,
	}, "")
	require.Equal(t, http.StatusOK, rec.Code)
	refreshToken := loginResp.Data.RefreshToken

	rec, refreshResp := doJSON(t, router, http.MethodPost, "/api/v1/auth/refresh", map[string]string{
		"refresh_token": refreshToken,
	}, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotEqual(t, refreshToken, refreshResp.Data.RefreshToken, "rotation must issue a new refresh token")

	// 4. Reuse of the OLD (pre-rotation) refresh token must be rejected.
	rec, _ = doJSON(t, router, http.MethodPost, "/api/v1/auth/refresh", map[string]string{
		"refresh_token": refreshToken,
	}, "")
	require.Equal(t, http.StatusUnauthorized, rec.Code, "reusing a rotated-away refresh token must be rejected")

	// 5. Logout with the NEW refresh token revokes it.
	rec, _ = doJSON(t, router, http.MethodPost, "/api/v1/auth/logout", map[string]string{
		"refresh_token": refreshResp.Data.RefreshToken,
	}, refreshResp.Data.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)

	rec, _ = doJSON(t, router, http.MethodPost, "/api/v1/auth/refresh", map[string]string{
		"refresh_token": refreshResp.Data.RefreshToken,
	}, "")
	require.Equal(t, http.StatusUnauthorized, rec.Code, "the logged-out refresh token must no longer work")

	// 6. Wrong password is rejected with a 401 and the standard envelope.
	rec, badLogin := doJSON(t, router, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": email, "password": "wrong-password",
	}, "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.False(t, badLogin.Success)
	require.NotNil(t, badLogin.Error)

	// 7. No Authorization header on a protected route.
	rec, _ = doJSON(t, router, http.MethodPost, "/api/v1/auth/logout", map[string]string{}, "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// 8. Deactivate the account, then confirm the FIRST access token
	// (still unexpired) no longer authenticates (master prompt §62).
	_, err = migrator.Exec(ctx, `UPDATE users SET status = 'inactive' WHERE email = $1`, email)
	require.NoError(t, err)
	rec, _ = doJSON(t, router, http.MethodPost, "/api/v1/auth/logout", map[string]string{}, firstAccess)
	require.Equal(t, http.StatusUnauthorized, rec.Code, "a deactivated user's still-unexpired access token must stop authenticating")
}
