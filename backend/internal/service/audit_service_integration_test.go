//go:build integration

// Run with: go test -tags=integration ./internal/service/...
// Requires the docker-compose postgres/redis services up, migrated,
// seeded. See case_service_integration_test.go for the shared helpers
// (migratorPool, appPool, truncateCaseTables, newUserWithRole, authUser,
// utilsAsAppError, discardLogger, paginationForTest) this file reuses.
//
// System 11's verification tests call AuditService.RunVerification
// DIRECTLY (synchronously, in the test goroutine) rather than going
// through a real Asynq worker/Redis round trip — RunVerification IS the
// exact function jobs.AuditVerificationHandler.ProcessTask calls, so
// testing it directly exercises the real verification logic without
// this package needing its own Asynq server; the full HTTP+real-worker
// flow is covered by internal/httpserver/audit_flow_integration_test.go.
package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	auditpkg "evidentia/backend/internal/audit"
	"evidentia/backend/internal/authz"
	"evidentia/backend/internal/jobs"
	"evidentia/backend/internal/models"
	"evidentia/backend/internal/realtime"
)

// newChainWriterForTest wires a real audit.ChainWriter against the live
// docker-compose PostgreSQL (the evidentia_app pool — the same
// unprivileged role production actually writes through).
func newChainWriterForTest(t *testing.T) *auditpkg.ChainWriter {
	t.Helper()
	return auditpkg.NewChainWriter(appPool(t), discardLogger())
}

// testJobClient builds a real jobs.Client against the docker-compose Redis
// — StartVerification's EnqueueVerifyAuditChain call needs a real
// connection even though these tests never run a worker to consume the
// resulting task (nothing here waits on that task actually completing;
// RunVerification is called directly instead — see this file's own
// package doc comment).
func testJobClient(t *testing.T) *jobs.Client {
	t.Helper()
	addr := envOr("REDIS_ADDR", "localhost:6379")
	client := jobs.NewClient(asynq.RedisClientOpt{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func newAuditServiceForTest(t *testing.T, recorder auditpkg.Recorder) *AuditService {
	t.Helper()
	appDB := appPool(t)
	authzService := authz.NewService(appDB, recorder)
	return NewAuditService(appDB, authzService, recorder, testJobClient(t), realtime.NewBroadcaster(), discardLogger())
}

// fetchAuditRowsOrdered reads every audit_log row (as the privileged
// migrator, bypassing RLS entirely) ordered by seq — a test-only,
// ground-truth view of the whole chain, never how production code itself
// reads the table.
func fetchAuditRowsOrdered(t *testing.T, pool *pgxpool.Pool) []auditRow {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT id, seq, "timestamp", user_id, role, action, resource_type, resource_id, case_id, metadata, prev_hash, hash
		FROM audit_log ORDER BY seq`)
	require.NoError(t, err)
	defer rows.Close()

	var out []auditRow
	for rows.Next() {
		var r auditRow
		require.NoError(t, rows.Scan(&r.ID, &r.Seq, &r.Timestamp, &r.UserID, &r.Role, &r.Action, &r.ResourceType, &r.ResourceID, &r.CaseID, &r.Metadata, &r.PrevHash, &r.Hash))
		out = append(out, r)
	}
	require.NoError(t, rows.Err())
	return out
}

type auditRow struct {
	ID           uuid.UUID
	Seq          int64
	Timestamp    time.Time
	UserID       *uuid.UUID
	Role         *string
	Action       string
	ResourceType string
	ResourceID   *uuid.UUID
	CaseID       *uuid.UUID
	Metadata     []byte
	PrevHash     []byte
	Hash         []byte
}

func TestChainWriter_GenesisAndSequentialChaining(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	writer := newChainWriterForTest(t)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "audit-chain1@example.com", models.RolePolice)

	writer.Record(ctx, auditpkg.Event{Action: "CASE_CREATED", ResourceType: "case", UserID: &officer, Role: models.RolePolice})
	writer.Record(ctx, auditpkg.Event{Action: "DOCUMENT_UPLOADED", ResourceType: "document", UserID: &officer, Role: models.RolePolice})
	writer.Record(ctx, auditpkg.Event{Action: "DOCUMENT_DOWNLOADED", ResourceType: "document", UserID: &officer, Role: models.RolePolice})

	rows := fetchAuditRowsOrdered(t, migrator)
	require.Len(t, rows, 3)

	assert.Nil(t, rows[0].PrevHash, "the first entry ever written must be the genesis entry (prev_hash NULL)")
	assert.Equal(t, rows[0].Hash, rows[1].PrevHash, "the second entry must reference the first entry's own hash")
	assert.Equal(t, rows[1].Hash, rows[2].PrevHash, "the third entry must reference the second entry's own hash")
	assert.Len(t, rows[0].Hash, auditpkg.EntryHashSize)
	assert.NotEqual(t, rows[0].Hash, rows[1].Hash)
	assert.NotEqual(t, rows[1].Hash, rows[2].Hash)
}

// TestChainWriter_ConcurrentWritesDoNotFork is the mandatory concurrency
// test: many goroutines call Record simultaneously; afterward, every
// committed entry must form ONE valid chain (no two entries sharing a
// prev_hash, no broken links) — run with -race.
func TestChainWriter_ConcurrentWritesDoNotFork(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	writer := newChainWriterForTest(t)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "audit-concurrent1@example.com", models.RolePolice)

	const writers = 40
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(n int) {
			defer wg.Done()
			writer.Record(ctx, auditpkg.Event{
				Action:       "DOCUMENT_DOWNLOADED",
				ResourceType: "document",
				UserID:       &officer,
				Role:         models.RolePolice,
				Metadata:     map[string]any{"n": n},
			})
		}(i)
	}
	wg.Wait()

	rows := fetchAuditRowsOrdered(t, migrator)
	require.Len(t, rows, writers, "every concurrent Record call must have committed exactly one entry — none lost, none duplicated")

	seenAsPrev := make(map[string]int)
	hashes := make(map[string]bool)
	genesisCount := 0
	for _, r := range rows {
		hashes[string(r.Hash)] = true
		if r.PrevHash == nil {
			genesisCount++
			continue
		}
		seenAsPrev[string(r.PrevHash)]++
	}
	assert.Equal(t, 1, genesisCount, "exactly one genesis entry")
	for prevHash, count := range seenAsPrev {
		assert.Equal(t, 1, count, "prev_hash %x must be claimed by exactly one entry — a count > 1 would mean a fork", []byte(prevHash))
	}
}

func TestAuditService_List_AdminSeesEverything(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	writer := newChainWriterForTest(t)
	svc := newAuditServiceForTest(t, writer)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "audit-list-officer1@example.com", models.RolePolice)
	admin := newUserWithRole(t, migrator, "audit-list-admin1@example.com", models.RoleAdmin)

	writer.Record(ctx, auditpkg.Event{Action: "CASE_CREATED", ResourceType: "case", UserID: &officer, Role: models.RolePolice})
	writer.Record(ctx, auditpkg.Event{Action: "DOCUMENT_UPLOADED", ResourceType: "document", UserID: &officer, Role: models.RolePolice})

	result, err := svc.List(ctx, authUser(admin, models.RoleAdmin), AuditListFilter{}, paginationForTest(1, 20))
	require.NoError(t, err)
	// +1 for this very List call's own AUDIT_ACCESSED event, recorded
	// AFTER the query ran (so it does not see itself).
	assert.GreaterOrEqual(t, len(result.Entries), 2)
	assert.GreaterOrEqual(t, result.Meta.Total, int64(2))
}

func TestAuditService_List_NonAdminSeesOnlyOwnActions(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	writer := newChainWriterForTest(t)
	svc := newAuditServiceForTest(t, writer)
	ctx := context.Background()

	officerA := newUserWithRole(t, migrator, "audit-list-officerA2@example.com", models.RolePolice)
	officerB := newUserWithRole(t, migrator, "audit-list-officerB2@example.com", models.RolePolice)

	writer.Record(ctx, auditpkg.Event{Action: "CASE_CREATED", ResourceType: "case", UserID: &officerA, Role: models.RolePolice})
	writer.Record(ctx, auditpkg.Event{Action: "CASE_CREATED", ResourceType: "case", UserID: &officerB, Role: models.RolePolice})

	result, err := svc.List(ctx, authUser(officerB, models.RolePolice), AuditListFilter{}, paginationForTest(1, 20))
	require.NoError(t, err)
	for _, e := range result.Entries {
		if e.UserID != nil {
			assert.Equal(t, officerB, *e.UserID, "officerB must never see officerA's audit entries via RLS")
		}
	}
}

func TestAuditService_List_ForensicsCannotAccess(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	writer := newChainWriterForTest(t)
	svc := newAuditServiceForTest(t, writer)
	ctx := context.Background()

	forensics := newUserWithRole(t, migrator, "audit-list-forensics3@example.com", models.RoleForensics)

	_, err := svc.List(ctx, authUser(forensics, models.RoleForensics), AuditListFilter{}, paginationForTest(1, 20))
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status, "FORENSICS holds no audit:read permission per the seed data")
}

func TestAuditService_List_FilterByAction(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	writer := newChainWriterForTest(t)
	svc := newAuditServiceForTest(t, writer)
	ctx := context.Background()

	admin := newUserWithRole(t, migrator, "audit-list-admin4@example.com", models.RoleAdmin)

	writer.Record(ctx, auditpkg.Event{Action: "CASE_CREATED", ResourceType: "case", UserID: &admin, Role: models.RoleAdmin})
	writer.Record(ctx, auditpkg.Event{Action: "DOCUMENT_UPLOADED", ResourceType: "document", UserID: &admin, Role: models.RoleAdmin})

	action := "CASE_CREATED"
	result, err := svc.List(ctx, authUser(admin, models.RoleAdmin), AuditListFilter{Action: &action}, paginationForTest(1, 20))
	require.NoError(t, err)
	require.NotEmpty(t, result.Entries)
	for _, e := range result.Entries {
		assert.Equal(t, "CASE_CREATED", e.Action)
	}
}

func TestChainWriter_CancelledContextDoesNotLeavePartialEntry(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	writer := newChainWriterForTest(t)

	officer := newUserWithRole(t, migrator, "audit-cancel-officer11@example.com", models.RolePolice)

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	// Record never returns an error (see its own doc comment) — this
	// call is expected to fail internally (the transaction cannot
	// proceed against an already-cancelled context) and be absorbed as a
	// logged, non-fatal failure.
	writer.Record(cancelledCtx, auditpkg.Event{Action: "DOCUMENT_DOWNLOADED", ResourceType: "document", UserID: &officer, Role: models.RolePolice})

	rows := fetchAuditRowsOrdered(t, migrator)
	assert.Empty(t, rows, "a write that fails before COMMIT must leave no row at all — no partial/phantom chain entry")
}

// ---- System 11: audit-chain verification job ----

// runVerificationToCompletion calls RunVerification directly (see this
// file's own package doc comment) and returns the final, terminal
// VerificationDetail via GetVerification.
func runVerificationToCompletion(t *testing.T, ctx context.Context, svc *AuditService, admin uuid.UUID, verificationID uuid.UUID) VerificationDetail {
	t.Helper()
	require.NoError(t, svc.RunVerification(ctx, verificationID))
	detail, err := svc.GetVerification(ctx, authUser(admin, models.RoleAdmin), verificationID)
	require.NoError(t, err)
	return *detail
}

func TestAuditService_StartVerification_NonAdminDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	writer := newChainWriterForTest(t)
	svc := newAuditServiceForTest(t, writer)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "verify-officer1@example.com", models.RolePolice)

	_, err := svc.StartVerification(ctx, authUser(officer, models.RolePolice))
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status, "audit:verify is ADMIN-only per the seed data")
}

func TestAuditService_GetVerification_NonAdminDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	writer := newChainWriterForTest(t)
	svc := newAuditServiceForTest(t, writer)
	ctx := context.Background()

	admin := newUserWithRole(t, migrator, "verify-admin-get1@example.com", models.RoleAdmin)
	officer := newUserWithRole(t, migrator, "verify-officer-get1@example.com", models.RolePolice)

	started, err := svc.StartVerification(ctx, authUser(admin, models.RoleAdmin))
	require.NoError(t, err)

	_, err = svc.GetVerification(ctx, authUser(officer, models.RolePolice), started.ID)
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status)
}

func TestAuditService_GetVerification_UnknownIDNotFound(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	writer := newChainWriterForTest(t)
	svc := newAuditServiceForTest(t, writer)
	ctx := context.Background()

	admin := newUserWithRole(t, migrator, "verify-admin-404@example.com", models.RoleAdmin)

	_, err := svc.GetVerification(ctx, authUser(admin, models.RoleAdmin), uuid.New())
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 404, appErr.Status)
}

func TestAuditService_StartAndRunVerification_FreshChainVerifies(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	writer := newChainWriterForTest(t)
	svc := newAuditServiceForTest(t, writer)
	ctx := context.Background()

	admin := newUserWithRole(t, migrator, "verify-admin5@example.com", models.RoleAdmin)
	for i := 0; i < 10; i++ {
		writer.Record(ctx, auditpkg.Event{Action: "DOCUMENT_DOWNLOADED", ResourceType: "document", UserID: &admin, Role: models.RoleAdmin})
	}

	started, err := svc.StartVerification(ctx, authUser(admin, models.RoleAdmin))
	require.NoError(t, err)
	assert.Equal(t, auditpkg.VerificationStatusQueued, started.Status)

	detail := runVerificationToCompletion(t, ctx, svc, admin, started.ID)
	assert.Equal(t, auditpkg.VerificationStatusVerified, detail.Status)
	assert.GreaterOrEqual(t, detail.EntriesChecked, int64(10))
	require.NotNil(t, detail.TotalEntries)
	assert.Equal(t, detail.EntriesChecked, *detail.TotalEntries)
	require.NotNil(t, detail.ProgressPercent)
	assert.InDelta(t, 100.0, *detail.ProgressPercent, 0.01)
	require.NotNil(t, detail.StartedAt)
	require.NotNil(t, detail.CompletedAt)
	assert.False(t, detail.CompletedAt.Before(*detail.StartedAt))
}

func TestAuditService_RunVerification_EmptyChainVerifies(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	writer := newChainWriterForTest(t)
	svc := newAuditServiceForTest(t, writer)
	ctx := context.Background()

	admin := newUserWithRole(t, migrator, "verify-empty-admin9@example.com", models.RoleAdmin)

	started, err := svc.StartVerification(ctx, authUser(admin, models.RoleAdmin))
	require.NoError(t, err)

	// StartVerification itself records an AUDIT_CHAIN_VERIFICATION_REQUESTED
	// event through the SAME shared audit.Recorder every other operation
	// uses (no special-cased "don't audit this one" exemption) — so by the
	// time RunVerification actually scans the chain, that ONE entry (the
	// request itself) already exists. A chain that was truly empty right
	// up until the very call that asks to verify it is not otherwise
	// reachable through the public API — a genuinely zero-row chain is
	// covered directly, without any database, by
	// TestVerifyBatch_EmptyChainIsVacuouslyValid in internal/audit.
	detail := runVerificationToCompletion(t, ctx, svc, admin, started.ID)
	assert.Equal(t, auditpkg.VerificationStatusVerified, detail.Status, "a chain of only its own request event is trivially valid")
	assert.Equal(t, int64(1), detail.EntriesChecked)
	require.NotNil(t, detail.TotalEntries)
	assert.Equal(t, int64(1), *detail.TotalEntries)
}

// TestAuditService_RunVerification_DetectsTamperingViaPrivilegedConnection
// is the mandatory tamper test: modify a committed row using a PRIVILEGED
// connection (the migrator role, which — unlike evidentia_app — is a
// superuser and can UPDATE audit_log directly, simulating an attacker who
// somehow obtained privileged database access, or an operator error) and
// confirm chain verification detects it. This does not weaken production
// permissions: evidentia_app itself still cannot UPDATE audit_log at all
// (see db_audit_privileges_test.go) — this uses a DIFFERENT, deliberately
// privileged connection specifically to prove the cryptographic chain is
// a genuine SECOND layer of defense.
func TestAuditService_RunVerification_DetectsTamperingViaPrivilegedConnection(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	writer := newChainWriterForTest(t)
	svc := newAuditServiceForTest(t, writer)
	ctx := context.Background()

	admin := newUserWithRole(t, migrator, "verify-tamper-admin7@example.com", models.RoleAdmin)
	for i := 0; i < 5; i++ {
		writer.Record(ctx, auditpkg.Event{Action: "DOCUMENT_DOWNLOADED", ResourceType: "document", UserID: &admin, Role: models.RoleAdmin})
	}

	pre, err := svc.StartVerification(ctx, authUser(admin, models.RoleAdmin))
	require.NoError(t, err)
	preDetail := runVerificationToCompletion(t, ctx, svc, admin, pre.ID)
	require.Equal(t, auditpkg.VerificationStatusVerified, preDetail.Status, "the chain must verify BEFORE tampering")

	// Tamper: as the migrator (privileged, RLS-exempt) role, directly
	// modify the action of the middle (3rd) entry.
	_, err = migrator.Exec(ctx, `UPDATE audit_log SET action = 'TAMPERED_ACTION' WHERE seq = 3`)
	require.NoError(t, err)

	post, err := svc.StartVerification(ctx, authUser(admin, models.RoleAdmin))
	require.NoError(t, err)
	postDetail := runVerificationToCompletion(t, ctx, svc, admin, post.ID)
	assert.Equal(t, auditpkg.VerificationStatusIntegrityFailure, postDetail.Status)
	require.NotNil(t, postDetail.FailedSeq)
	assert.Equal(t, int64(3), *postDetail.FailedSeq)
	assert.Equal(t, auditpkg.FailureTypeEntryHashMismatch, postDetail.FailureType)
	assert.NotEmpty(t, postDetail.FailureReason)
}

func TestAuditService_RunVerification_DetectsDeletedEntry(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	writer := newChainWriterForTest(t)
	svc := newAuditServiceForTest(t, writer)
	ctx := context.Background()

	admin := newUserWithRole(t, migrator, "verify-tamper-admin8@example.com", models.RoleAdmin)
	for i := 0; i < 5; i++ {
		writer.Record(ctx, auditpkg.Event{Action: "DOCUMENT_DOWNLOADED", ResourceType: "document", UserID: &admin, Role: models.RoleAdmin})
	}

	_, err := migrator.Exec(ctx, `DELETE FROM audit_log WHERE seq = 3`)
	require.NoError(t, err)

	started, err := svc.StartVerification(ctx, authUser(admin, models.RoleAdmin))
	require.NoError(t, err)
	detail := runVerificationToCompletion(t, ctx, svc, admin, started.ID)
	assert.Equal(t, auditpkg.VerificationStatusIntegrityFailure, detail.Status, "deleting a committed entry must be detected as a broken chain link")
	assert.Equal(t, auditpkg.FailureTypePreviousHashMismatch, detail.FailureType)
}

// TestAuditService_StartVerification_DeduplicatesActiveRun is the
// mandatory "simultaneous verification requests" concurrency test at the
// StartVerification level: many goroutines call StartVerification at
// once, before any of them has been run to completion. Exactly one
// audit_verifications row must exist afterward, and every goroutine must
// have received THAT SAME id — the database-level
// idx_audit_verifications_single_active unique index is what actually
// guarantees this (see AuditService.StartVerification's own doc comment),
// not application-level locking.
func TestAuditService_StartVerification_DeduplicatesActiveRun(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	writer := newChainWriterForTest(t)
	svc := newAuditServiceForTest(t, writer)
	ctx := context.Background()

	admin := newUserWithRole(t, migrator, "verify-dedup-admin1@example.com", models.RoleAdmin)
	user := authUser(admin, models.RoleAdmin)

	const callers = 20
	ids := make([]uuid.UUID, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func(n int) {
			defer wg.Done()
			result, err := svc.StartVerification(ctx, user)
			errs[n] = err
			if err == nil {
				ids[n] = result.ID
			}
		}(i)
	}
	wg.Wait()

	for _, err := range errs {
		require.NoError(t, err)
	}
	first := ids[0]
	for _, id := range ids {
		assert.Equal(t, first, id, "every concurrent StartVerification call must return the SAME active verification id")
	}

	var count int
	require.NoError(t, migrator.QueryRow(ctx, `SELECT count(*) FROM audit_verifications`).Scan(&count))
	assert.Equal(t, 1, count, "exactly one verification row may exist while one is QUEUED/RUNNING — idx_audit_verifications_single_active must have rejected every other insert attempt")
}

// TestAuditService_RunVerification_ConcurrentInvocationsDoNotCorruptState
// is the "multiple workers" concurrency test: simulates several worker
// goroutines all picking up (calling RunVerification on) the SAME
// verification_id concurrently — e.g. a redelivered/duplicate Asynq task.
// MarkAuditVerificationRunning's `AND status = 'QUEUED'` guard must let
// exactly ONE of them actually perform the verification; every other
// call must see the row already RUNNING/terminal and return immediately
// (nil error, no re-verification, no corrupted counters).
func TestAuditService_RunVerification_ConcurrentInvocationsDoNotCorruptState(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	writer := newChainWriterForTest(t)
	svc := newAuditServiceForTest(t, writer)
	ctx := context.Background()

	admin := newUserWithRole(t, migrator, "verify-multiworker-admin1@example.com", models.RoleAdmin)
	for i := 0; i < 8; i++ {
		writer.Record(ctx, auditpkg.Event{Action: "DOCUMENT_DOWNLOADED", ResourceType: "document", UserID: &admin, Role: models.RoleAdmin})
	}

	started, err := svc.StartVerification(ctx, authUser(admin, models.RoleAdmin))
	require.NoError(t, err)
	// +1 beyond the 8 explicit writes above: StartVerification's own
	// AUDIT_CHAIN_VERIFICATION_REQUESTED event is itself now part of the
	// chain by the time RunVerification scans it.
	const wantEntries = 9

	const attempts = 10
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func() {
			defer wg.Done()
			assert.NoError(t, svc.RunVerification(ctx, started.ID))
		}()
	}
	wg.Wait()

	detail, err := svc.GetVerification(ctx, authUser(admin, models.RoleAdmin), started.ID)
	require.NoError(t, err)
	assert.Equal(t, auditpkg.VerificationStatusVerified, detail.Status)
	assert.Equal(t, int64(wantEntries), detail.EntriesChecked, "concurrent duplicate RunVerification invocations must never double-count entries_checked")
}

func TestAuditService_MarkVerificationOperationallyFailed(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	writer := newChainWriterForTest(t)
	svc := newAuditServiceForTest(t, writer)
	ctx := context.Background()

	admin := newUserWithRole(t, migrator, "verify-opfail-admin1@example.com", models.RoleAdmin)
	started, err := svc.StartVerification(ctx, authUser(admin, models.RoleAdmin))
	require.NoError(t, err)

	require.NoError(t, svc.MarkVerificationOperationallyFailed(ctx, started.ID, assertAnError{}))

	detail, err := svc.GetVerification(ctx, authUser(admin, models.RoleAdmin), started.ID)
	require.NoError(t, err)
	assert.Equal(t, auditpkg.VerificationStatusFailed, detail.Status, "an operational failure must be FAILED, never INTEGRITY_FAILURE — an outage is not evidence of tampering")
	assert.Equal(t, auditpkg.OperationalFailureDatabaseError, detail.FailureType)
	assert.NotEmpty(t, detail.FailureReason)
	require.NotNil(t, detail.CompletedAt)
}

type assertAnError struct{}

func (assertAnError) Error() string { return "simulated operational failure" }

func TestAuditService_ReconcileStale_RunningWithoutProgressBecomesFailed(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	writer := newChainWriterForTest(t)
	svc := newAuditServiceForTest(t, writer)
	ctx := context.Background()

	admin := newUserWithRole(t, migrator, "verify-stale-admin1@example.com", models.RoleAdmin)
	started, err := svc.StartVerification(ctx, authUser(admin, models.RoleAdmin))
	require.NoError(t, err)

	// Simulate a worker that picked the job up, then died mid-run: mark it
	// RUNNING with an updated_at far enough in the past to exceed
	// staleRunningThreshold, using the privileged migrator connection
	// (application code never backdates a timestamp like this).
	_, err = migrator.Exec(ctx, `
		UPDATE audit_verifications
		SET status = 'RUNNING', started_at = now() - interval '10 minutes',
		    updated_at = now() - interval '10 minutes'
		WHERE id = $1`, started.ID)
	require.NoError(t, err)

	detail, err := svc.GetVerification(ctx, authUser(admin, models.RoleAdmin), started.ID)
	require.NoError(t, err)
	assert.Equal(t, auditpkg.VerificationStatusFailed, detail.Status, "a RUNNING verification with no progress for longer than expected must be reconciled to FAILED, never left RUNNING forever, and never falsely VERIFIED")
	assert.Equal(t, auditpkg.OperationalFailureStaleTimeout, detail.FailureType)

	// The correction must be PERSISTED, not merely returned — a second,
	// independent read must see the identical terminal state.
	var status string
	require.NoError(t, migrator.QueryRow(ctx, `SELECT status FROM audit_verifications WHERE id = $1`, started.ID).Scan(&status))
	assert.Equal(t, auditpkg.VerificationStatusFailed, status)
}

func TestAuditService_GetIntegritySummary(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	writer := newChainWriterForTest(t)
	svc := newAuditServiceForTest(t, writer)
	ctx := context.Background()

	admin := newUserWithRole(t, migrator, "verify-summary-admin1@example.com", models.RoleAdmin)
	for i := 0; i < 3; i++ {
		writer.Record(ctx, auditpkg.Event{Action: "DOCUMENT_DOWNLOADED", ResourceType: "document", UserID: &admin, Role: models.RoleAdmin})
	}

	started, err := svc.StartVerification(ctx, authUser(admin, models.RoleAdmin))
	require.NoError(t, err)
	runVerificationToCompletion(t, ctx, svc, admin, started.ID)

	summary, err := svc.GetIntegritySummary(ctx, authUser(admin, models.RoleAdmin))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, summary.TotalEntries, int64(3))
	require.NotNil(t, summary.ChainHeadSeq)
	assert.NotEmpty(t, summary.ChainHeadHash)
	require.NotNil(t, summary.LastVerification)
	assert.Equal(t, started.ID, summary.LastVerification.ID)
	assert.Equal(t, auditpkg.VerificationStatusVerified, summary.LastVerification.Status)
}

func TestAuditService_GetIntegritySummary_NonAdminDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	writer := newChainWriterForTest(t)
	svc := newAuditServiceForTest(t, writer)
	ctx := context.Background()

	lawyer := newUserWithRole(t, migrator, "verify-summary-lawyer1@example.com", models.RoleLawyer)

	_, err := svc.GetIntegritySummary(ctx, authUser(lawyer, models.RoleLawyer))
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status)
}
