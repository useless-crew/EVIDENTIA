package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"evidentia/backend/db/generated"
	"evidentia/backend/internal/models"
	"evidentia/backend/internal/repository"
)

// chainWriterRLSRole is the app.role ChainWriter's own internal
// chain-head lookup (GetLatest) establishes for RLS purposes — see its
// use in append's doc comment for why this must be ADMIN regardless of
// the real event's actor/role.
const chainWriterRLSRole = models.RoleAdmin

// auditChainLockKey is the fixed PostgreSQL advisory-lock key ChainWriter
// uses to serialize concurrent chain-head reads/writes (see
// db/queries/audit.sql's AcquireAuditChainLock). An arbitrary constant
// reserved solely for this purpose — it names no row, table, or other
// resource, and never needs to change or be coordinated with anything
// else in the schema.
const auditChainLockKey int64 = 891273465019

// systemActorID is a reserved, non-nil UUID used ONLY as the transaction-
// local RLS identity (app.user_id) when an event has no real UserID
// (e.g. AUTH_LOGIN_FAILED for an email matching no account at all — see
// AuthService.Login's "unknown_email" case). It is NEVER written into
// audit_log.user_id itself — that column stores the event's REAL UserID,
// left nil when there is none (nullable specifically for this — see the
// schema's own column comment: "a system-initiated action... still gets
// an entry") — and never needs to correspond to a real users row, since
// audit_log_insert's RLS policy only checks current_app_user_id() IS NOT
// NULL, never that the value references an existing user.
// repository.AppIdentity{} (the actual zero value) would instead set
// app.user_id to NULL (WithTx's documented "no identity" behavior),
// which would make the INSERT itself fail that RLS check — this
// sentinel exists so a genuinely no-real-user event can still be
// durably chain-recorded rather than silently dropped to the
// operational log only.
var systemActorID = uuid.MustParse("00000000-0000-0000-0000-00000000a001")

// maxChainWriteAttempts bounds ChainWriter's retry loop against
// idx_audit_log_prev_hash_unique/idx_audit_log_single_genesis — a pure
// defense-in-depth backstop (auditChainLockKey's advisory lock already
// prevents two transactions from ever computing the same prev_hash in
// the first place), never relied upon as the primary concurrency-safety
// mechanism.
const maxChainWriteAttempts = 3

// ChainWriter is the authoritative audit.Recorder implementation: every
// Record call durably appends one cryptographically-chained row to
// audit_log, inside a single PostgreSQL transaction that also
// serializes itself against every other concurrent Record call. It
// implements the same audit.Recorder interface audit.SlogRecorder
// (System 3's original, operational-log-only placeholder) already
// satisfies — every existing caller across Systems 3-9 (auth/case/
// document/certificate/redact/share services) starts writing to the
// real, tamper-evident chain the moment this type is wired into
// app.New in place of SlogRecorder, with no change to any of those
// callers or to the Recorder interface itself.
type ChainWriter struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func NewChainWriter(pool *pgxpool.Pool, logger *slog.Logger) *ChainWriter {
	return &ChainWriter{pool: pool, logger: logger}
}

// Record implements audit.Recorder. Per that interface's own doc
// comment (established by System 3, unchanged here): it never returns
// an error, and a caller must never fail its own operation merely
// because audit recording had a problem. A failure here (database
// unavailable, an exhausted retry loop) is logged operationally at
// ERROR level with full diagnostic detail — the one place an audit-
// write failure becomes visible at all — and otherwise silently
// absorbed, exactly as every existing caller across Systems 3-9 already
// assumes when it calls recorder.Record with no return value to check.
func (w *ChainWriter) Record(ctx context.Context, event Event) {
	if err := w.append(ctx, event); err != nil {
		w.logger.ErrorContext(ctx, "audit chain write failed — event was NOT recorded",
			slog.String("action", event.Action),
			slog.String("resource_type", event.ResourceType),
			slog.String("error", err.Error()),
		)
	}
}

func (w *ChainWriter) append(ctx context.Context, event Event) error {
	canonicalMetadata, err := canonicalizeEventMetadata(event.Metadata)
	if err != nil {
		return fmt.Errorf("audit: %w", err)
	}

	// Role is deliberately chainWriterRLSRole (ADMIN), NOT event.Role: this
	// transaction's own GetLatest read of the chain head is scoped by
	// audit_log_select, which restricts a non-ADMIN identity to rows it
	// owns (or a shared case) — exactly the rows OTHER actors previously
	// wrote would be invisible to. Since ChainWriter must always see the
	// TRUE, complete chain head to compute a correct prev_hash (not "the
	// latest entry this particular actor happens to be allowed to see"),
	// it establishes ADMIN's own already-legitimate unrestricted-visibility
	// branch of that SAME RLS policy for the query it runs internally here
	// — it does not bypass RLS, and no other RLS-protected table is ever
	// touched inside this transaction, so no broader visibility leaks to
	// the caller. event.Role (the real actor's role) is untouched and is
	// still what gets written into the audit_log row itself, below.
	ident := repository.AppIdentity{Role: chainWriterRLSRole}
	if event.UserID != nil {
		ident.UserID = *event.UserID
	} else {
		ident.UserID = systemActorID
	}

	var lastErr error
	for attempt := 0; attempt < maxChainWriteAttempts; attempt++ {
		txErr := repository.WithTx(ctx, w.pool, ident, func(ctx context.Context, q *generated.Queries) error {
			repo := repository.NewAuditRepo(q)

			// Serializes this transaction against every other concurrent
			// ChainWriter.append call, for its ENTIRE remaining duration
			// (released automatically at COMMIT/ROLLBACK) — see the
			// underlying query's doc comment. Acquired BEFORE reading the
			// chain head, so no other transaction can read the same head
			// this one is about to claim as its predecessor.
			if err := repo.AcquireChainLock(ctx, auditChainLockKey); err != nil {
				return fmt.Errorf("acquire chain lock: %w", err)
			}

			var prevHash []byte
			latest, err := repo.GetLatest(ctx)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				prevHash = nil // this transaction is creating the genesis entry
			case err != nil:
				return fmt.Errorf("read chain head: %w", err)
			default:
				prevHash = latest.Hash
			}

			id := uuid.New()
			// Truncated to microseconds — PostgreSQL's timestamptz
			// resolution — so the value hashed here and the value later
			// read back from audit_log.timestamp are bit-for-bit
			// identical (the same reasoning CertificateService's
			// issuedAt already documents).
			ts := time.Now().UTC().Truncate(time.Microsecond)

			entryHash := ComputeEntryHash(Entry{
				ID:           id,
				Timestamp:    ts,
				UserID:       event.UserID,
				Role:         event.Role,
				Action:       event.Action,
				ResourceType: event.ResourceType,
				ResourceID:   event.ResourceID,
				CaseID:       event.CaseID,
				Metadata:     canonicalMetadata,
				PrevHash:     prevHash,
			})

			_, err = repo.Insert(ctx, generated.InsertAuditEntryParams{
				ID:           id,
				Timestamp:    ts,
				UserID:       event.UserID, // the event's REAL user, nil when there is none — never systemActorID
				Role:         nilIfEmpty(event.Role),
				Action:       event.Action,
				ResourceType: event.ResourceType,
				ResourceID:   event.ResourceID,
				CaseID:       event.CaseID,
				Metadata:     canonicalMetadata,
				PrevHash:     prevHash,
				Hash:         entryHash,
			})
			return err
		})
		if txErr == nil {
			return nil
		}
		lastErr = txErr
		if !isChainConflict(txErr) {
			return txErr
		}
		// Extremely unlikely given auditChainLockKey's advisory lock
		// already serializes writers — retried anyway per the explicit
		// requirement that two concurrent audit events must never
		// accidentally use the same previous hash, so a rare conflict is
		// never surfaced as a permanent failure without at least trying
		// again against the now-current head.
	}
	return fmt.Errorf("audit: chain write: exhausted %d attempts: %w", maxChainWriteAttempts, lastErr)
}

// canonicalizeEventMetadata marshals a caller-supplied event map through
// the SAME CanonicalizeMetadata function verification uses on whatever
// comes back from PostgreSQL — so both the value hashed/stored at write
// time and the value re-hashed at verify time are produced by, and
// therefore guaranteed to agree via, one single code path. A fresh Go
// map marshaled via encoding/json is already deterministic (sorted
// keys), so this round-trip costs little and buys a strong invariant:
// there is no separate "write-side canonicalization" to keep in sync
// with CanonicalizeMetadata by hand.
func canonicalizeEventMetadata(m map[string]any) (json.RawMessage, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal metadata: %w", err)
	}
	return CanonicalizeMetadata(raw)
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// isChainConflict reports whether err is a PostgreSQL unique-violation
// (SQLSTATE 23505) on one of audit_log's chain-integrity constraints —
// the signal that another transaction won the race to claim the chain
// head this attempt was about to use, and a retry (against the new,
// now-current head) is the correct response rather than treating it as
// a permanent failure.
func isChainConflict(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	if pgErr.Code != "23505" {
		return false
	}
	switch pgErr.ConstraintName {
	case "idx_audit_log_prev_hash_unique", "idx_audit_log_single_genesis", "audit_log_hash_unique":
		return true
	default:
		return false
	}
}
