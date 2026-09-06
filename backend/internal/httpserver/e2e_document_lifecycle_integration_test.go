//go:build integration

// Run with: go test -tags=integration ./internal/httpserver/...
// Requires the full docker-compose infrastructure up and migrated — see
// auth_flow_integration_test.go's doc comment for the shared -p 1 note.
//
// System 16: every feature exercised below (upload, verify, certificate,
// redact, share/revoke, audit, chain verification) already has its own
// dedicated flow test in this package — this file's purpose is
// deliberately different and additive, not a duplicate: it drives ONE
// document through its ENTIRE real lifecycle in a single continuous run,
// the way an investigator actually would, so a regression in how two
// features COMPOSE (e.g. a redacted derivative's certificate, or a
// revoked share's effect on delegated access) would be caught even if
// each feature's own isolated test still passes individually.
package httpserver

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/app"
)

// TestE2E_FullDocumentLifecycle drives the master prompt's 26-step user
// journey (bootstrap-equivalent admin/role creation through case
// creation, evidence upload, cross-role access control, verification,
// certification, redaction, sharing/revocation, and audit-chain
// verification) against the real router, real Postgres/Redis/MinIO, and
// a real embedded Asynq worker — never a mock of any of these.
func TestE2E_FullDocumentLifecycle(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	application, err := app.New(ctx)
	require.NoError(t, err)
	defer application.Close()
	router := NewRouter(application)

	// A real embedded Asynq worker — step 24/25/26 (chain verification)
	// must be processed by the actual background job, never a direct
	// RunVerification call bypassing the queue.
	shutdownWorker := newTestAuditWorker(t, application)
	defer shutdownWorker()

	migrator, err := pgxpool.New(ctx, "postgres://evidentia:changeme_example@localhost:5432/evidentia?sslmode=disable")
	require.NoError(t, err)
	defer migrator.Close()

	const password = "correct horse battery staple"
	suffix := uuid.New().String()[:8]

	// ---- 1-6: bootstrap-equivalent admin + one user per role ----
	// (Real bootstrap (internal/bootstrap) is exercised at process
	// startup from EVIDENTIA_BOOTSTRAP_ADMIN_* env vars — see
	// docs/DEPLOYMENT.md — and admin_flow_integration_test.go's
	// TestAdminFlow_PrivilegeEscalationMatrix already proves ADMIN
	// creates every role through the real POST /admin/users endpoint.
	// Seeding directly here keeps this file focused on what it uniquely
	// adds: the document's own lifecycle, not re-proving admin creation.)
	adminEmail := "e2e-admin-" + suffix + "@example.com"
	policeEmail := "e2e-police-" + suffix + "@example.com"
	forensicsEmail := "e2e-forensics-" + suffix + "@example.com"
	lawyerEmail := "e2e-lawyer-" + suffix + "@example.com"
	judgeEmail := "e2e-judge-" + suffix + "@example.com" // unrelated to the case below — the "unauthorized role" of step 13/14

	seedCaseTestUser(t, migrator, adminEmail, "ADMIN", password)
	seedCaseTestUser(t, migrator, policeEmail, "POLICE", password)
	seedCaseTestUser(t, migrator, forensicsEmail, "FORENSICS", password)
	seedCaseTestUser(t, migrator, lawyerEmail, "LAWYER", password)
	seedCaseTestUser(t, migrator, judgeEmail, "JUDGE", password)

	// ---- 7: Police logs in ----
	adminToken := loginAs(t, router, adminEmail, password)
	policeToken := loginAs(t, router, policeEmail, password)
	forensicsToken := loginAs(t, router, forensicsEmail, password)
	lawyerToken := loginAs(t, router, lawyerEmail, password)
	judgeToken := loginAs(t, router, judgeEmail, password)

	// ---- 8: Police creates a case ----
	rec, caseResp := doJSONWithHeaders(t, router, http.MethodPost, "/api/v1/cases", map[string]any{
		"case_number": "E2E-" + suffix, "title": "E2E lifecycle case",
	}, policeToken, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	caseID := caseResp.Data.ID

	// ---- 9-10: Police uploads evidence; hash is computed and stored
	// server-side (never client-supplied) ----
	pngBytes := redactFlowTestPNG(t)
	rec, uploaded := doUpload(t, router, caseID, policeToken, "OTHER", "E2E evidence photo", "evidence.png", pngBytes, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	documentID := uploaded.Data.ID
	originalHash := uploaded.Data.Sha256Hash
	require.Len(t, originalHash, 64)

	// Link FORENSICS to the case (no public API grants case membership
	// directly — see abac_test.go's identical pattern — this exercises
	// the SAME case_members row RLS/ABAC actually reads).
	_, err = migrator.Exec(ctx,
		`INSERT INTO case_members (case_id, user_id, membership_type, added_by) VALUES ($1, (SELECT id FROM users WHERE email = $2), 'FORENSICS', (SELECT id FROM users WHERE email = $3))`,
		caseID, forensicsEmail, policeEmail)
	require.NoError(t, err)

	// ---- 11: Forensics (now linked) accesses the authorized case/document ----
	rec2 := doDownload(t, router, documentID, forensicsToken)
	require.Equal(t, http.StatusOK, rec2.Code)

	// ---- 12: Lawyer, NOT yet shared with and not case-linked, has no
	// access to this specific document yet (Systems 4/9: LAWYER's
	// document:read/download permission alone is not a case relationship
	// or a share) ----
	rec2 = doDownload(t, router, documentID, lawyerToken)
	require.Equal(t, http.StatusForbidden, rec2.Code)

	// ---- 13-14: an unrelated role (JUDGE, never linked to this case)
	// attempts access and is denied ----
	rec2 = doDownload(t, router, documentID, judgeToken)
	require.Equal(t, http.StatusForbidden, rec2.Code)

	// ---- 15: Evidence is verified — server recomputes the hash from the
	// actual stored object, never trusting a client-submitted value ----
	rec, verifyResp := doVerify(t, router, documentID, policeToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "VERIFIED", verifyResp.Data.Status)
	require.Equal(t, originalHash, verifyResp.Data.StoredHash)
	require.Equal(t, originalHash, verifyResp.Data.ComputedHash)

	// ---- 16: Certificate is generated/accessed, bound to the exact
	// document hash just verified. certificate:read is not in POLICE's
	// permission set today (only ADMIN/JUDGE — see the seed role/
	// permission matrix), so this uses adminToken, not policeToken. ----
	rec, certResp := doCertificate(t, router, documentID, adminToken)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, documentID, certResp.Data.DocumentID)
	require.Equal(t, originalHash, certResp.Data.DocumentHash)
	originalCertSignature := certResp.Data.Signature

	// ---- 17-18: Document is redacted (ADMIN-only per current RBAC —
	// see document_redact_flow_integration_test.go). The derivative
	// receives a new document record and a new hash. ----
	rec, redactResp := doRedact(t, router, documentID, adminToken, map[string]any{
		"reason": "Protect witness identity",
		"regions": []map[string]any{
			{"page": 1, "x": 0, "y": 0, "width": 10, "height": 10},
		},
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	redactedDocumentID := redactResp.Data.Document.ID
	redactedHash := redactResp.Data.Document.Sha256Hash
	require.NotEqual(t, originalHash, redactedHash, "the redacted derivative must have an independent hash from the original")
	require.NotNil(t, redactResp.Data.Document.ParentDocumentID)
	require.Equal(t, documentID, *redactResp.Data.Document.ParentDocumentID, "lineage must point back to the exact source document")

	// The ORIGINAL is untouched: re-verifying it still reports the same
	// hash it always has, not the derivative's.
	rec, reverify := doVerify(t, router, documentID, policeToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, originalHash, reverify.Data.StoredHash, "redacting a derivative must never mutate the original's stored hash")

	// The derivative gets its OWN, independent certificate — never a
	// reused/forwarded copy of the original's.
	rec, redactedCert := doCertificate(t, router, redactedDocumentID, adminToken)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, redactedHash, redactedCert.Data.DocumentHash)
	require.NotEqual(t, originalCertSignature, redactedCert.Data.Signature, "a redacted derivative must never share the original's certificate signature")

	// ---- 19-20: Document is shared (the ORIGINAL, view permission) with
	// LAWYER; recipient accesses it via the delegated grant ----
	var lawyerUserID string
	require.NoError(t, migrator.QueryRow(ctx, `SELECT id FROM users WHERE email = $1`, lawyerEmail).Scan(&lawyerUserID))

	rec, shareResp := doShare(t, router, documentID, policeToken, map[string]any{
		"user_id": lawyerUserID, "permission": "VIEW",
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	shareID := shareResp.Data.ShareID

	rec2 = doDownload(t, router, documentID, lawyerToken)
	require.Equal(t, http.StatusOK, rec2.Code, "the share must grant the recipient real download access to the ORIGINAL")

	// ---- 21-22: Share is revoked; recipient loses delegated access ----
	rec2 = doRevokeShare(t, router, documentID, shareID, policeToken)
	require.Equal(t, http.StatusOK, rec2.Code)

	rec2 = doDownload(t, router, documentID, lawyerToken)
	require.Equal(t, http.StatusForbidden, rec2.Code, "a revoked share must immediately deny the recipient's further access")

	// ---- 23: Audit records all of the above — every action this test
	// performed within this ONE case appears in ADMIN's audit view, each
	// carrying a real, non-empty hash. Filtered by case_id, not
	// resource_id=documentID: CERTIFICATE_CREATED/DOCUMENT_SHARED/
	// DOCUMENT_SHARE_REVOKED are correctly recorded against their OWN
	// resource (the certificate/share's own id, resource_type
	// compliance_certificate/document_share — see CertificateService/
	// ShareService), not the document's id, so only case_id captures all
	// of them together. ----
	rec, auditForCase := doAuditList(t, router, adminToken, "case_id="+caseID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	seenActions := map[string]bool{}
	for _, e := range auditForCase.Data.Entries {
		seenActions[e.Action] = true
		require.Len(t, e.Hash, 64)
	}
	for _, wantAction := range []string{"DOCUMENT_UPLOADED", "DOCUMENT_VERIFIED", "CERTIFICATE_CREATED", "DOCUMENT_REDACTED", "DOCUMENT_SHARED"} {
		require.Truef(t, seenActions[wantAction], "expected %s to appear in this case's audit trail, got actions: %v", wantAction, seenActions)
	}

	// DOCUMENT_SHARE_REVOKED specifically: unlike DOCUMENT_SHARED, this
	// event does not carry case_id (see ShareService.RevokeShare), so it
	// is checked by its own resource_id (the share's id) instead.
	rec, auditForRevoke := doAuditList(t, router, adminToken, "resource_id="+shareID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	foundRevoked := false
	for _, e := range auditForRevoke.Data.Entries {
		if e.Action == "DOCUMENT_SHARE_REVOKED" {
			foundRevoked = true
		}
	}
	require.True(t, foundRevoked, "DOCUMENT_SHARE_REVOKED must be recorded against the share's own resource_id")

	rec, auditForRedaction := doAuditList(t, router, adminToken, "resource_id="+redactedDocumentID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	foundRedacted := false
	for _, e := range auditForRedaction.Data.Entries {
		if e.Action == "DOCUMENT_REDACTED" {
			foundRedacted = true
		}
	}
	require.True(t, foundRedacted, "DOCUMENT_REDACTED must be recorded against the DERIVATIVE's own resource_id")

	// ---- 24-26: Audit chain verification runs (via the real embedded
	// worker, exactly as SSE/REST would report it in production) and
	// completes successfully, proving every entry this test just
	// generated is itself chained correctly. ----
	rec, startResp := doStartVerification(t, router, adminToken)
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	final := pollVerificationUntilTerminal(t, router, adminToken, startResp.Data.VerificationID)
	require.Equal(t, "VERIFIED", final.Data.Status, "the chain, including every entry this lifecycle test just wrote, must verify intact")
}
