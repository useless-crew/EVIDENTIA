//go:build integration

// Run with: go test -tags=integration ./internal/httpserver/...
// Requires the full docker-compose infrastructure (postgres, redis, minio)
// up and migrated (including 000004_document_sharing)/seeded — see
// auth_flow_integration_test.go's doc comment for the shared -p 1 note.
//
// This exercises the full master prompt §64 end-to-end demo flow through
// the fully wired application via real HTTP requests: POLICE uploads,
// shares with LAWYER, LAWYER sees it in "Shared With Me" and downloads
// it, POLICE revokes, LAWYER is denied — plus the IDOR/direct-URL-
// manipulation checks §16 requires explicitly. Complements
// internal/service's ShareService-level tests (which cover the fuller
// authorization/permission-tier matrix directly).
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
)

type shareEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		ShareID         string  `json:"share_id"`
		DocumentID      string  `json:"document_id"`
		RecipientUserID string  `json:"recipient_user_id"`
		Permission      string  `json:"permission"`
		Status          string  `json:"status"`
		EffectiveStatus string  `json:"effective_status"`
		ExpiresAt       *string `json:"expires_at"`
	} `json:"data"`
	Error *errorEnvelope `json:"error"`
}

type sharesListEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		Shares []struct {
			ShareID         string `json:"share_id"`
			EffectiveStatus string `json:"effective_status"`
		} `json:"shares"`
	} `json:"data"`
	Error *errorEnvelope `json:"error"`
}

type sharedWithMeEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		Documents []struct {
			ShareID  string `json:"share_id"`
			Document struct {
				ID       string `json:"id"`
				Filename string `json:"filename"`
			} `json:"document"`
		} `json:"documents"`
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	} `json:"data"`
	Error *errorEnvelope `json:"error"`
}

type userSearchEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		Users []struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"users"`
	} `json:"data"`
	Error *errorEnvelope `json:"error"`
}

func doShare(t *testing.T, router http.Handler, documentID, bearer string, body map[string]any) (*httptest.ResponseRecorder, shareEnvelope) {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/documents/"+documentID+"/share", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var env shareEnvelope
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	}
	return rec, env
}

func doListShares(t *testing.T, router http.Handler, documentID, bearer string) (*httptest.ResponseRecorder, sharesListEnvelope) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/documents/"+documentID+"/shares", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var env sharesListEnvelope
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	}
	return rec, env
}

func doRevokeShare(t *testing.T, router http.Handler, documentID, shareID, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/documents/"+documentID+"/shares/"+shareID+"/revoke", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func doSharedWithMe(t *testing.T, router http.Handler, bearer string) (*httptest.ResponseRecorder, sharedWithMeEnvelope) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/shared/documents", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var env sharedWithMeEnvelope
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	}
	return rec, env
}

func doUserSearch(t *testing.T, router http.Handler, q, bearer string) (*httptest.ResponseRecorder, userSearchEnvelope) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/search?q="+q, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var env userSearchEnvelope
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	}
	return rec, env
}

func TestDocumentShareFlow_EndToEndDemo(t *testing.T) {
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
	policeEmail := "share-http-police-" + suffix + "@example.com"
	lawyerEmail := "share-http-lawyer-" + suffix + "@example.com"
	forensicsEmail := "share-http-forensics-" + suffix + "@example.com"

	// 1-2. Admin creates POLICE/LAWYER users — approximated here by
	// direct seeding (System 8's admin-user-creation HTTP flow is already
	// covered by admin_flow_integration_test.go; this test focuses on
	// sharing itself).
	seedCaseTestUser(t, migrator, policeEmail, "POLICE", password)
	seedCaseTestUser(t, migrator, lawyerEmail, "LAWYER", password)
	seedCaseTestUser(t, migrator, forensicsEmail, "FORENSICS", password)
	policeToken := loginAs(t, router, policeEmail, password)
	lawyerToken := loginAs(t, router, lawyerEmail, password)
	forensicsToken := loginAs(t, router, forensicsEmail, password)

	// 3-6. Police accesses an authorized case and uploads evidence.
	rec, caseResp := doJSONWithHeaders(t, router, http.MethodPost, "/api/v1/cases", map[string]any{
		"case_number": "SHARE-HTTP-" + suffix, "title": "Sharing flow case",
	}, policeToken, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	caseID := caseResp.Data.ID

	rec, uploaded := doUpload(t, router, caseID, policeToken, "WITNESS_STATEMENT", "", "evidence.txt", []byte("shared evidence content"), nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	documentID := uploaded.Data.ID

	// Recipient search (master prompt §38/§48): Police looks up Lawyer by
	// email/name substring.
	rec, searchResp := doUserSearch(t, router, "share-http-lawyer-"+suffix, policeToken)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, searchResp.Data.Users, 1)
	lawyerUserID := searchResp.Data.Users[0].ID
	require.Equal(t, lawyerEmail, searchResp.Data.Users[0].Email)

	// A too-short query is rejected rather than dumping every user.
	rec, _ = doUserSearch(t, router, "a", policeToken)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// 7-14. Police opens the document, shares it (VIEW), sets no
	// expiration, backend validates and creates the share, audit is
	// recorded (verified at the service layer already — this asserts the
	// HTTP-visible outcome).
	rec, shared := doShare(t, router, documentID, policeToken, map[string]any{
		"user_id":    lawyerUserID,
		"permission": "VIEW",
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.True(t, shared.Success)
	require.Equal(t, documentID, shared.Data.DocumentID)
	require.Equal(t, lawyerUserID, shared.Data.RecipientUserID)
	require.Equal(t, "ACTIVE", shared.Data.Status)
	shareID := shared.Data.ShareID
	require.NotEmpty(t, shareID)
	require.NotContains(t, rec.Body.String(), "minio", "no storage backend detail may leak into the response")

	// 15. Police sees the active share.
	rec, listResp := doListShares(t, router, documentID, policeToken)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, listResp.Data.Shares, 1)
	require.Equal(t, "ACTIVE", listResp.Data.Shares[0].EffectiveStatus)

	// IDOR: FORENSICS was never given access to this case/document and
	// holds no document:share permission — cannot see its share list,
	// cannot create a share on it, cannot revoke it.
	rec, _ = doListShares(t, router, documentID, forensicsToken)
	require.Equal(t, http.StatusForbidden, rec.Code)
	rec, _ = doShare(t, router, documentID, forensicsToken, map[string]any{"user_id": lawyerUserID, "permission": "VIEW"})
	require.Equal(t, http.StatusForbidden, rec.Code)
	rec = doRevokeShare(t, router, documentID, shareID, forensicsToken)
	require.Equal(t, http.StatusForbidden, rec.Code)

	// IDOR: a share ID that is real, but revoked through a GUESSED,
	// unrelated document ID, is denied at the document-access gate
	// itself — the same generic 403 RequireDocumentAccess already
	// returns for any nonexistent/unrelated document (this codebase's
	// established anti-enumeration posture: a resource this caller has
	// no relationship to is indistinguishable from one that doesn't
	// exist at all — see internal/authz's CanAccessDocument). It never
	// leaks that the share ID itself is valid, and never reaches
	// ShareService.RevokeShare's OWN 404 (which only fires once the
	// document itself is confirmed accessible but the specific share
	// row isn't).
	rec = doRevokeShare(t, router, uuid.New().String(), shareID, policeToken)
	require.Equal(t, http.StatusForbidden, rec.Code)

	// 16-18. Lawyer logs in, sees the document in "Shared With Me".
	rec, sharedWithMe := doSharedWithMe(t, router, lawyerToken)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, sharedWithMe.Data.Documents, 1)
	require.Equal(t, documentID, sharedWithMe.Data.Documents[0].Document.ID)
	require.Equal(t, "evidence.txt", sharedWithMe.Data.Documents[0].Document.Filename)

	// 19-21. Backend validates the active share; Lawyer downloads
	// (VIEW permission allows it); download is audit logged (verified at
	// the service layer).
	downloadRec := doDownload(t, router, documentID, lawyerToken)
	require.Equal(t, http.StatusOK, downloadRec.Code)
	require.Equal(t, []byte("shared evidence content"), downloadRec.Body.Bytes())

	// VIEW does not imply VERIFY (master prompt §24) — LAWYER additionally
	// holds no document:verify RBAC permission at all, so this is denied
	// regardless of the share's permission tier.
	rec, _ = doVerify(t, router, documentID, lawyerToken, nil)
	require.Equal(t, http.StatusForbidden, rec.Code)

	// 22. Police revokes the share.
	rec = doRevokeShare(t, router, documentID, shareID, policeToken)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// 23-24. Lawyer tries to open/download the document — access denied.
	downloadRec = doDownload(t, router, documentID, lawyerToken)
	require.Equal(t, http.StatusForbidden, downloadRec.Code)

	// "Shared With Me" no longer lists it either.
	rec, sharedWithMe = doSharedWithMe(t, router, lawyerToken)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, sharedWithMe.Data.Documents)

	// Revoking again is a documented no-op-as-404, never a 500 or a
	// silent "still active".
	rec = doRevokeShare(t, router, documentID, shareID, policeToken)
	require.Equal(t, http.StatusNotFound, rec.Code)
}
