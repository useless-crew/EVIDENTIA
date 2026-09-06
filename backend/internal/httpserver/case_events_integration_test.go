//go:build integration

// Run with: go test -tags=integration ./internal/httpserver/...
// Requires the full docker-compose infrastructure (postgres, redis, minio)
// up and migrated/seeded — see auth_flow_integration_test.go's doc
// comment for the shared -p 1 note.
//
// This exercises System 13's new GET /cases/:id/events route end to end:
// a real Server-Sent-Events connection (httptest.Server, not
// httptest.ResponseRecorder, which cannot stream — same reasoning as
// audit_flow_integration_test.go's TestAuditFlow_SSE) receiving a real
// SHARE_CREATED notification published by ShareService.CreateShare after
// its own transaction commits, PLUS the mandatory cross-case isolation
// check: an event published for a DIFFERENT case must never reach this
// case's stream, and a user with no relationship to the case must never
// be allowed to connect at all.
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

func TestCaseEvents_SSE_DeliversShareCreatedAndEnforcesIsolation(t *testing.T) {
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
	officerEmail := "case-events-officer-" + suffix + "@example.com"
	recipientEmail := "case-events-recipient-" + suffix + "@example.com"
	outsiderEmail := "case-events-outsider-" + suffix + "@example.com"
	seedCaseTestUser(t, migrator, officerEmail, "POLICE", password)
	seedCaseTestUser(t, migrator, recipientEmail, "POLICE", password)
	seedCaseTestUser(t, migrator, outsiderEmail, "POLICE", password)

	officerToken := loginAs(t, router, officerEmail, password)
	outsiderToken := loginAs(t, router, outsiderEmail, password)

	// Case A: the officer's own case, and an UNRELATED case B — proves
	// cross-case isolation below.
	rec, caseA := doJSONWithHeaders(t, router, http.MethodPost, "/api/v1/cases", map[string]any{
		"case_number": "CASE-EVENTS-A-" + suffix, "title": "Case A",
	}, officerToken, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	caseAID := caseA.Data.ID

	rec, caseB := doJSONWithHeaders(t, router, http.MethodPost, "/api/v1/cases", map[string]any{
		"case_number": "CASE-EVENTS-B-" + suffix, "title": "Case B",
	}, officerToken, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	caseBID := caseB.Data.ID

	// An outsider with no relationship to case A must never even connect.
	outsiderReq, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/cases/"+caseAID+"/events", nil)
	require.NoError(t, err)
	outsiderReq.Header.Set("Authorization", "Bearer "+outsiderToken)
	outsiderResp, err := http.DefaultClient.Do(outsiderReq)
	require.NoError(t, err)
	defer outsiderResp.Body.Close()
	require.Equal(t, http.StatusForbidden, outsiderResp.StatusCode, "a user with no relationship to the case must never be allowed to open its event stream")

	// The officer connects to case A's real event stream.
	sseCtx, sseCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer sseCancel()
	sseReq, err := http.NewRequestWithContext(sseCtx, http.MethodGet, server.URL+"/api/v1/cases/"+caseAID+"/events", nil)
	require.NoError(t, err)
	sseReq.Header.Set("Authorization", "Bearer "+officerToken)
	sseResp, err := http.DefaultClient.Do(sseReq)
	require.NoError(t, err)
	defer sseResp.Body.Close()
	require.Equal(t, http.StatusOK, sseResp.StatusCode)
	require.Contains(t, sseResp.Header.Get("Content-Type"), "text/event-stream")

	received := make(chan string, 4)
	go func() {
		scanner := bufio.NewScanner(sseResp.Body)
		var lastEventType string
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "event: ") {
				lastEventType = strings.TrimPrefix(line, "event: ")
			}
			if strings.HasPrefix(line, "data: ") {
				var decoded events.Event
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &decoded) == nil {
					select {
					case received <- lastEventType + ":" + decoded.ResourceID:
					default:
					}
				}
			}
		}
	}()

	// Give the SSE connection a moment to actually register before
	// publishing — the same real subscriber-attach timing caveat
	// internal/sse's own integration test documents.
	time.Sleep(300 * time.Millisecond)

	// Upload a document to case A, then share it — ShareService.CreateShare
	// publishes SHARE_CREATED scoped to case A AFTER its own transaction
	// commits.
	rec2, uploadResp := doUpload(t, router, caseAID, officerToken, "OTHER", "", "evidence.txt", []byte("case events evidence"), nil)
	require.Equal(t, http.StatusCreated, rec2.Code, rec2.Body.String())
	documentID := uploadResp.Data.ID

	searchRec, searchEnv := doUserSearch(t, router, recipientEmail, officerToken)
	require.Equal(t, http.StatusOK, searchRec.Code, searchRec.Body.String())
	require.NotEmpty(t, searchEnv.Data.Users, "recipient must be discoverable via user search")
	recipientID := searchEnv.Data.Users[0].ID

	rec3, shareResp := doShare(t, router, documentID, officerToken, map[string]any{
		"user_id": recipientID, "permission": "VIEW",
	})
	require.Equal(t, http.StatusCreated, rec3.Code, rec3.Body.String())
	_ = shareResp

	select {
	case got := <-received:
		require.Equal(t, events.TypeShareCreated+":"+caseAID, got, "case A's own stream must receive the SHARE_CREATED event scoped to case A")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the real SHARE_CREATED event on case A's stream")
	}

	// Cross-case isolation: an event published for UNRELATED case B must
	// never reach case A's stream. Upload+share a document in case B and
	// confirm nothing further arrives on case A's channel.
	rec4, uploadRespB := doUpload(t, router, caseBID, officerToken, "OTHER", "", "case-b-evidence.txt", []byte("case B evidence"), nil)
	require.Equal(t, http.StatusCreated, rec4.Code, rec4.Body.String())
	rec5, _ := doShare(t, router, uploadRespB.Data.ID, officerToken, map[string]any{
		"user_id": recipientID, "permission": "VIEW",
	})
	require.Equal(t, http.StatusCreated, rec5.Code, rec5.Body.String())

	select {
	case got := <-received:
		t.Fatalf("case A's stream received an event scoped to a DIFFERENT case — cross-case leak: %s", got)
	case <-time.After(1 * time.Second):
	}
}
