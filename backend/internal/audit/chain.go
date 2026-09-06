// Package audit (this file): the cryptographic hash-chain core — one
// canonical entry representation and one hash function, used identically
// by chain writing (writer.go) and chain verification (verifier.go).
// Neither of those ever computes a hash any other way; this is the
// single source of truth master prompt's "one canonical hashing
// function... never duplicate hashing logic" requires.
package audit

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// EntryHashSize is the length in bytes of a SHA-256 digest —
// audit_log_hash_length_check (and, for a non-genesis entry,
// audit_log_prev_hash_length_check) constrain the stored columns to
// exactly this.
const EntryHashSize = sha256.Size

// Entry is the exact set of fields ComputeEntryHash commits to — every
// field an attacker would need to leave unchanged to forge a valid
// entry undetected. Deliberately excludes Seq: audit_log.seq is a
// PostgreSQL `GENERATED ALWAYS AS IDENTITY` column, assigned atomically
// by the database at INSERT time — a value that fundamentally cannot be
// known (let alone hashed) before the row is written, unlike ID/Timestamp
// below, which the writer generates in Go and supplies explicitly to
// INSERT specifically so they CAN be hashed first (see
// db/queries/audit.sql's InsertAuditEntry comment). Excluding Seq from
// the hash does not weaken the chain: chain ORDER is independently
// verified by walking rows in seq order and checking PrevHash linkage
// (see verifier.go) — Seq itself is never treated as attacker-controlled
// input a forger could otherwise substitute.
type Entry struct {
	ID           uuid.UUID
	Timestamp    time.Time
	UserID       *uuid.UUID
	Role         string
	Action       string
	ResourceType string
	ResourceID   *uuid.UUID
	CaseID       *uuid.UUID
	// Metadata must already be canonicalized (see CanonicalizeMetadata) —
	// ComputeEntryHash does not re-canonicalize it, so a caller that
	// passes non-canonical bytes here would compute a hash that could
	// legitimately fail to reproduce later. Both writer.go and
	// verifier.go always call CanonicalizeMetadata first.
	Metadata json.RawMessage
	// PrevHash is nil for exactly one entry in the whole chain — the
	// genesis entry (audit_log.prev_hash IS NULL, enforced unique by
	// idx_audit_log_single_genesis — see the schema migration). Every
	// other entry's PrevHash is the previous entry's own Hash.
	PrevHash []byte
}

// CanonicalizeMetadata normalizes raw into the ONE deterministic byte
// representation this package ever hashes or stores — solving master
// prompt's "database driver formatting"/"unstable serialization" concern
// for the JSONB metadata column specifically: PostgreSQL's jsonb storage
// does not guarantee preserving the exact input byte layout (key order,
// whitespace) of what was originally inserted, so hashing "whatever the
// database happens to hand back" would make verification depend on
// PostgreSQL's internal jsonb text-output format rather than the actual
// content. The fix: decode into a Go map and re-encode via
// encoding/json.Marshal, which is DOCUMENTED to sort object keys
// deterministically (recursively, for nested objects too) — regardless
// of the input's original key order or whitespace. Applying this at BOTH
// insert time (before hashing/storing) and verify time (on whatever was
// read back) means the two always agree: canonicalizing an
// already-canonical value is idempotent. nil/empty input canonicalizes
// to "{}", never the JSON literal "null" (keeps one single empty-metadata
// representation, matching the "{}" convention already used elsewhere in
// this codebase for empty JSONB metadata, e.g. DocumentService.UploadDocument).
func CanonicalizeMetadata(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage("{}"), nil
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("audit: canonicalize metadata: %w", err)
	}
	if v == nil {
		return json.RawMessage("{}"), nil
	}
	canonical, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("audit: canonicalize metadata: %w", err)
	}
	return canonical, nil
}

// ComputeEntryHash returns entry's SHA-256 digest: a fixed-field-order,
// labeled canonical string (the same "line-per-field, deterministic
// order and format" pattern internal/service/certificate_service.go's
// canonicalCertificatePayload already established for exactly this
// reason — never Go struct memory layout, never map iteration order,
// never arbitrary json.Marshal field ordering on the entry itself).
// PrevHash is included as ONE of these fields — never appended a second
// time after — so "entry_hash = SHA256(canonical_entry_without_entry_hash
// + previous_hash)" is satisfied without ever double-counting prev_hash
// or including the entry's own (not-yet-computed) hash in its own input.
//
// This is the ONLY function in this codebase that computes an audit
// entry hash — writer.go (on insert) and verifier.go (on verification)
// both call this exact function, never a re-implementation, so the two
// can never silently drift apart.
func ComputeEntryHash(entry Entry) []byte {
	var b strings.Builder
	b.WriteString("evidentia-audit-entry\n")
	fmt.Fprintf(&b, "id=%s\n", entry.ID)
	fmt.Fprintf(&b, "timestamp=%s\n", entry.Timestamp.UTC().Format(time.RFC3339Nano))
	fmt.Fprintf(&b, "user_id=%s\n", uuidOrEmpty(entry.UserID))
	fmt.Fprintf(&b, "role=%s\n", entry.Role)
	fmt.Fprintf(&b, "action=%s\n", entry.Action)
	fmt.Fprintf(&b, "resource_type=%s\n", entry.ResourceType)
	fmt.Fprintf(&b, "resource_id=%s\n", uuidOrEmpty(entry.ResourceID))
	fmt.Fprintf(&b, "case_id=%s\n", uuidOrEmpty(entry.CaseID))
	fmt.Fprintf(&b, "metadata=%s\n", string(entry.Metadata))
	fmt.Fprintf(&b, "prev_hash=%s", prevHashOrGenesis(entry.PrevHash))

	sum := sha256.Sum256([]byte(b.String()))
	return sum[:]
}

func uuidOrEmpty(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

// genesisMarker is the literal token embedded in the canonical string in
// place of a hex-encoded prev_hash for the one entry that has none — an
// explicit, documented placeholder (never an empty string, which could
// otherwise be confused with "the hex encoding of zero bytes" or a
// formatting accident) so genesis is unambiguous to both a human reading
// the canonicalization and to ComputeEntryHash itself.
const genesisMarker = "genesis"

func prevHashOrGenesis(prevHash []byte) string {
	if len(prevHash) == 0 {
		return genesisMarker
	}
	return fmt.Sprintf("%x", prevHash)
}
