//go:build integration

// Run with: go test -tags=integration ./internal/httpserver/...
// Requires the full docker-compose infrastructure (postgres, redis, minio)
// up and migrated/seeded — see auth_flow_integration_test.go's doc
// comment for the shared -p 1 note.
//
// This exercises System 14's new GET /admin/users/events route end to
// end: a real Server-Sent-Events connection receiving a real
// USER_CREATED notification published by UserService.CreateUser after
// its own transaction commits, plus the mandatory authorization check —
// a non-admin must never even be allowed to connect (same pattern as
// case_events_integration_test.go's TestCaseEvents_SSE_..., applied to
// this system's own new endpoint).
package httpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/app"
	"evidentia/backend/internal/events"
)

func TestAdminUsersEvents_SSE_DeliversUserCreatedAndEnforcesAuthorization(t *testing.T) {
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

	sseManagerCtx, sseManagerCancel := context.WithCancel(context.Background())
	defer sseManagerCancel()
	go application.SSEManager.Start(sseManagerCtx)

	server := httptest.NewServer(router)
	defer server.Close()

	migrator, err := pgxpool.New(ctx, "postgres://evidentia:changeme_example@localhost:5432/evidentia?sslmode=disable")
	require.NoError(t, err)
	defer migrator.Close()

	const password = "correct horse battery staple"
	suffix := uuid.New().String()[:8]
	adminEmail := "admin-events-admin-" + suffix + "@example.com"
	policeEmail := "admin-events-police-" + suffix + "@example.com"
	seedCaseTestUser(t, migrator, adminEmail, "ADMIN", password)
	seedCaseTestUser(t, migrator, policeEmail, "POLICE", password)

	adminToken := loginAs(t, router, adminEmail, password)
	policeToken := loginAs(t, router, policeEmail, password)

	// A non-admin must never be allowed to connect at all.
	policeReq, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/admin/users/events", nil)
	require.NoError(t, err)
	policeReq.Header.Set("Authorization", "Bearer "+policeToken)
	policeResp, err := http.DefaultClient.Do(policeReq)
	require.NoError(t, err)
	defer policeResp.Body.Close()
	require.Equal(t, http.StatusForbidden, policeResp.StatusCode, "only user:read (ADMIN-only) may open the admin user-management event stream")

	// Unauthenticated connection must be rejected too.
	unauthResp, err := http.Get(server.URL + "/api/v1/admin/users/events")
	require.NoError(t, err)
	defer unauthResp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, unauthResp.StatusCode)

	// The admin connects to the real event stream.
	sseCtx, sseCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer sseCancel()
	sseReq, err := http.NewRequestWithContext(sseCtx, http.MethodGet, server.URL+"/api/v1/admin/users/events", nil)
	require.NoError(t, err)
	sseReq.Header.Set("Authorization", "Bearer "+adminToken)
	sseResp, err := http.DefaultClient.Do(sseReq)
	require.NoError(t, err)
	defer sseResp.Body.Close()
	require.Equal(t, http.StatusOK, sseResp.StatusCode)
	require.Contains(t, sseResp.Header.Get("Content-Type"), "text/event-stream")

	received := make(chan events.Event, 4)
	go func() {
		scanner := bufio.NewScanner(sseResp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				var decoded events.Event
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &decoded) == nil {
					select {
					case received <- decoded:
					default:
					}
				}
			}
		}
	}()

	// Give the SSE connection a moment to actually register before
	// creating the user — matches the same real subscriber-attach timing
	// caveat documented in internal/sse's own integration test.
	time.Sleep(300 * time.Millisecond)

	createStatus, createEnv := doUserJSON(t, router, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"email": "admin-events-created-" + suffix + "@example.com", "password": "a-valid-password-1",
		"first_name": "Event", "last_name": "Target", "role": "POLICE",
	}, adminToken)
	require.Equal(t, http.StatusCreated, createStatus)

	select {
	case got := <-received:
		require.Equal(t, events.TypeUserCreated, got.EventType)
		require.Equal(t, events.ResourceTypeAdminUsers, got.ResourceType)
		require.NotContains(t, string(got.Data), "password", "the event payload must never contain a password/password_hash field")
		var data events.AdminUserEventData
		require.NoError(t, json.Unmarshal(got.Data, &data))
		require.Equal(t, createEnv.Data.ID, data.UserID)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the real USER_CREATED event on the admin user-management stream")
	}
}
