//go:build integration

// Run with: go test -tags=integration ./internal/httpserver/...
// Requires the full docker-compose infrastructure up and migrated/seeded —
// see auth_flow_integration_test.go's doc comment for the shared -p 1
// note (this file also touches the shared users/user_roles/auth_sessions
// tables).
//
// This is System 8's HTTP-level privilege-escalation matrix (master
// prompt §18): every /admin/* and /users/me route, driven through real
// HTTP requests exactly as a client would experience them, complementing
// internal/service's UserService-level tests (business logic directly)
// and backend/tests' RLS-level tests.
package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/app"
)

type userEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		ID     string   `json:"id"`
		Email  string   `json:"email"`
		Roles  []string `json:"roles"`
		Status string   `json:"status"`
		Users  []struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"users"`
	} `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func doUserJSON(t *testing.T, router http.Handler, method, path string, body any, bearer string) (int, userEnvelope) {
	t.Helper()
	rec, raw := doJSONRaw(t, router, method, path, body, bearer, nil)
	var env userEnvelope
	if len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, &env))
	}
	return rec.Code, env
}

// TestAdminFlow_PrivilegeEscalationMatrix exercises master prompt §18's
// full list against the live router: every one of POLICE/FORENSICS/
// LAWYER/JUDGE must be denied on every /admin/* route, unauthenticated
// and garbage-token requests must be 401, and self-role/self-status
// modification must be denied even for ADMIN.
func TestAdminFlow_PrivilegeEscalationMatrix(t *testing.T) {
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

	suffix := uuid.New().String()[:8]
	const password = "correct horse battery staple"

	adminEmail := "admin-flow-" + suffix + "@example.com"
	seedCaseTestUser(t, migrator, adminEmail, "ADMIN", password)
	adminToken := loginAs(t, router, adminEmail, password)

	roleEmails := map[string]string{}
	roleTokens := map[string]string{}
	for _, role := range []string{"POLICE", "FORENSICS", "LAWYER", "JUDGE"} {
		email := "flow-" + role + "-" + suffix + "@example.com"
		seedCaseTestUser(t, migrator, email, role, password)
		roleEmails[role] = email
		roleTokens[role] = loginAs(t, router, email, password)
	}

	// A target user (POLICE) whose id every mutating route below acts on.
	targetEmail := "flow-target-" + suffix + "@example.com"
	seedCaseTestUser(t, migrator, targetEmail, "POLICE", password)
	_, listResp := doUserJSON(t, router, http.MethodGet, "/api/v1/admin/users?search="+targetEmail, nil, adminToken)
	require.Len(t, listResp.Data.Users, 1, "the freshly seeded target user must be findable by ADMIN")
	targetID := listResp.Data.Users[0].ID

	adminMutatingRequests := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"create", http.MethodPost, "/api/v1/admin/users", map[string]any{"email": "escapee-" + suffix + "@example.com", "password": "irrelevant1", "first_name": "A", "last_name": "B", "role": "POLICE"}},
		{"list", http.MethodGet, "/api/v1/admin/users", nil},
		{"get", http.MethodGet, "/api/v1/admin/users/" + targetID, nil},
		{"update", http.MethodPut, "/api/v1/admin/users/" + targetID, map[string]any{"first_name": "X", "last_name": "Y"}},
		{"role", http.MethodPut, "/api/v1/admin/users/" + targetID + "/role", map[string]any{"role": "JUDGE"}},
		{"status", http.MethodPut, "/api/v1/admin/users/" + targetID + "/status", map[string]any{"status": "inactive"}},
		{"password", http.MethodPut, "/api/v1/admin/users/" + targetID + "/password", map[string]any{"password": "new-password-1"}},
	}

	// ---- Unauthenticated: every route is 401 ----
	for _, r := range adminMutatingRequests {
		code, _ := doUserJSON(t, router, r.method, r.path, r.body, "")
		require.Equalf(t, http.StatusUnauthorized, code, "unauthenticated %s %s must be 401", r.method, r.path)
	}

	// ---- Garbage/invalid bearer token: every route is 401 ----
	for _, r := range adminMutatingRequests {
		code, _ := doUserJSON(t, router, r.method, r.path, r.body, "not-a-real-jwt.at.all")
		require.Equalf(t, http.StatusUnauthorized, code, "invalid-token %s %s must be 401", r.method, r.path)
	}

	// ---- Every non-admin role is denied on every admin route ----
	for role, token := range roleTokens {
		for _, r := range adminMutatingRequests {
			code, env := doUserJSON(t, router, r.method, r.path, r.body, token)
			require.Equalf(t, http.StatusForbidden, code, "%s must be denied on %s %s", role, r.method, r.path)
			require.NotNil(t, env.Error)
			require.Equal(t, "FORBIDDEN", env.Error.Code)
		}
	}

	// ---- GET /admin/roles requires only authentication — any role may
	// call it (docs/API_ENDPOINTS.md). Its data is a bare array, unlike
	// every other admin response here, so it gets its own envelope type. ----
	rec, rawRoles := doJSONRaw(t, router, http.MethodGet, "/api/v1/admin/roles", nil, roleTokens["POLICE"], nil)
	require.Equal(t, http.StatusOK, rec.Code, string(rawRoles))
	var rolesEnv struct {
		Success bool `json:"success"`
		Data    []struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rawRoles, &rolesEnv))
	require.True(t, rolesEnv.Success)
	require.NotEmpty(t, rolesEnv.Data)

	// ---- GET /users/me requires only authentication — any role may view
	// their own profile. ----
	code, meResp := doUserJSON(t, router, http.MethodGet, "/api/v1/users/me", nil, roleTokens["LAWYER"])
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, roleEmails["LAWYER"], meResp.Data.Email)

	// ---- ADMIN happy path: create one user per role ----
	createdIDs := map[string]string{}
	for _, role := range []string{"ADMIN", "POLICE", "FORENSICS", "LAWYER", "JUDGE"} {
		code, resp := doUserJSON(t, router, http.MethodPost, "/api/v1/admin/users", map[string]any{
			"email": "created-" + role + "-" + suffix + "@example.com", "password": "a-fresh-password-1",
			"first_name": "New", "last_name": role, "role": role,
		}, adminToken)
		require.Equalf(t, http.StatusCreated, code, "ADMIN must be able to create a %s user", role)
		require.Equal(t, []string{role}, resp.Data.Roles)
		createdIDs[role] = resp.Data.ID
	}

	// ---- ADMIN cannot change their OWN role or status via HTTP ----
	_, adminSelf := doUserJSON(t, router, http.MethodGet, "/api/v1/users/me", nil, adminToken)
	code, _ = doUserJSON(t, router, http.MethodPut, "/api/v1/admin/users/"+adminSelf.Data.ID+"/role", map[string]any{"role": "POLICE"}, adminToken)
	require.Equal(t, http.StatusForbidden, code, "an ADMIN must not be able to change their own role, even via a direct HTTP call")

	code, _ = doUserJSON(t, router, http.MethodPut, "/api/v1/admin/users/"+adminSelf.Data.ID+"/status", map[string]any{"status": "inactive"}, adminToken)
	require.Equal(t, http.StatusForbidden, code, "an ADMIN must not be able to deactivate their own account")

	// ---- ADMIN deactivates the target user; the target can no longer log
	// in afterward ----
	code, _ = doUserJSON(t, router, http.MethodPut, "/api/v1/admin/users/"+targetID+"/status", map[string]any{"status": "inactive"}, adminToken)
	require.Equal(t, http.StatusOK, code)

	rec, _ = doJSON(t, router, http.MethodPost, "/api/v1/auth/login", map[string]string{"email": targetEmail, "password": password}, "")
	require.Equal(t, http.StatusUnauthorized, rec.Code, "a deactivated user must be rejected at login")

	// ---- ADMIN resets the created POLICE user's password; the new
	// password then works for login ----
	newPassword := "reset-password-here-1"
	code, _ = doUserJSON(t, router, http.MethodPut, "/api/v1/admin/users/"+createdIDs["POLICE"]+"/password", map[string]any{"password": newPassword}, adminToken)
	require.Equal(t, http.StatusNoContent, code)

	rec, loginResp := doJSON(t, router, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "created-POLICE-" + suffix + "@example.com", "password": newPassword,
	}, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotEmpty(t, loginResp.Data.AccessToken)

	// ---- ADMIN changes the created LAWYER user's role to JUDGE ----
	code, roleResp := doUserJSON(t, router, http.MethodPut, "/api/v1/admin/users/"+createdIDs["LAWYER"]+"/role", map[string]any{"role": "JUDGE"}, adminToken)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, []string{"JUDGE"}, roleResp.Data.Roles)
}
