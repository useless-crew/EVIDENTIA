package audit

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"evidentia/backend/db/generated"
)

// buildChain constructs n valid, correctly hash-chained generated.AuditLog
// rows in memory (genesis first), using the exact same ComputeEntryHash
// function the real writer/verifier both use — so these tests exercise
// the real chain-construction invariant, not a hand-rolled approximation
// of it.
func buildChain(t *testing.T, n int) []generated.AuditLog {
	t.Helper()
	entries := make([]generated.AuditLog, 0, n)
	var prevHash []byte
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	for i := 0; i < n; i++ {
		id := uuid.New()
		ts := time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC)
		metadata, err := CanonicalizeMetadata(json.RawMessage(`{"n":` + strconv.Itoa(i) + `}`))
		require.NoError(t, err)

		hash := ComputeEntryHash(Entry{
			ID:           id,
			Timestamp:    ts,
			UserID:       &userID,
			Role:         "POLICE",
			Action:       "DOCUMENT_UPLOADED",
			ResourceType: "document",
			ResourceID:   nil,
			CaseID:       nil,
			Metadata:     metadata,
			PrevHash:     prevHash,
		})

		seq := int64(i + 1)
		entries = append(entries, generated.AuditLog{
			ID:           id,
			Seq:          &seq,
			Timestamp:    ts,
			UserID:       &userID,
			Role:         strPtr("POLICE"),
			Action:       "DOCUMENT_UPLOADED",
			ResourceType: "document",
			ResourceID:   nil,
			CaseID:       nil,
			Metadata:     metadata,
			PrevHash:     prevHash,
			Hash:         hash,
		})
		prevHash = hash
	}
	return entries
}

func strPtr(s string) *string { return &s }

func TestVerifyBatch_ValidChain(t *testing.T) {
	entries := buildChain(t, 5)
	result := VerifyBatch(entries, nil)
	assert.True(t, result.OK)
	assert.Equal(t, 5, result.EntriesChecked)
	assert.Equal(t, entries[4].Hash, result.LastHash)
}

func TestVerifyBatch_EmptyChainIsVacuouslyValid(t *testing.T) {
	result := VerifyBatch(nil, nil)
	assert.True(t, result.OK)
	assert.Equal(t, 0, result.EntriesChecked)
}

func TestVerifyBatch_GenesisMustHaveNilPrevHash(t *testing.T) {
	entries := buildChain(t, 1)
	// Corrupt: the genesis entry now claims a non-nil predecessor.
	entries[0].PrevHash = make([]byte, EntryHashSize)

	result := VerifyBatch(entries, nil)
	require.False(t, result.OK)
	assert.Equal(t, entries[0].ID, *result.FailedEntryID)
	assert.Contains(t, result.Reason, "prev_hash")
}

func TestVerifyBatch_DetectsTampering(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(entries []generated.AuditLog)
	}{
		{"action modified", func(e []generated.AuditLog) { e[2].Action = "DOCUMENT_DELETED" }},
		{"user_id modified", func(e []generated.AuditLog) {
			other := uuid.New()
			e[2].UserID = &other
		}},
		{"timestamp modified", func(e []generated.AuditLog) { e[2].Timestamp = e[2].Timestamp.Add(time.Hour) }},
		{"resource_type modified", func(e []generated.AuditLog) { e[2].ResourceType = "case" }},
		{"metadata modified", func(e []generated.AuditLog) { e[2].Metadata = json.RawMessage(`{"n":9999}`) }},
		{"prev_hash modified", func(e []generated.AuditLog) { e[2].PrevHash = make([]byte, EntryHashSize) }},
		{"hash modified", func(e []generated.AuditLog) { e[2].Hash = make([]byte, EntryHashSize) }},
		{"role modified", func(e []generated.AuditLog) { e[2].Role = strPtr("ADMIN") }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries := buildChain(t, 5)
			tc.mutate(entries)

			result := VerifyBatch(entries, nil)
			require.False(t, result.OK, "tampering (%s) must be detected", tc.name)
			require.NotNil(t, result.FailedEntryID)
			assert.Equal(t, entries[2].ID, *result.FailedEntryID)
		})
	}
}

func TestVerifyBatch_DeletedEntryBreaksChain(t *testing.T) {
	entries := buildChain(t, 5)
	// Simulate deletion of entry index 2: the next entry's prev_hash no
	// longer matches anything in the remaining sequence.
	withoutOne := append(append([]generated.AuditLog{}, entries[:2]...), entries[3:]...)

	result := VerifyBatch(withoutOne, nil)
	require.False(t, result.OK)
	assert.Equal(t, entries[3].ID, *result.FailedEntryID, "the entry immediately after the deleted one must be where verification fails")
}

func TestVerifyBatch_InsertedForkEntryDetected(t *testing.T) {
	entries := buildChain(t, 3)
	forged := generated.AuditLog{
		ID:           uuid.New(),
		Seq:          entries[1].Seq,
		Timestamp:    entries[1].Timestamp,
		UserID:       entries[1].UserID,
		Role:         entries[1].Role,
		Action:       "FORGED_ACTION",
		ResourceType: entries[1].ResourceType,
		Metadata:     entries[1].Metadata,
		PrevHash:     entries[0].Hash, // claims to follow entry 0, same as the real entry 1
		Hash:         make([]byte, EntryHashSize),
	}
	forked := []generated.AuditLog{entries[0], forged, entries[1], entries[2]}

	result := VerifyBatch(forked, nil)
	require.False(t, result.OK, "an inserted/forked entry must be detected")
}

func TestVerifyBatch_ResumesAcrossBatchesViaLastHash(t *testing.T) {
	entries := buildChain(t, 6)

	first := VerifyBatch(entries[:3], nil)
	require.True(t, first.OK)

	second := VerifyBatch(entries[3:], first.LastHash)
	require.True(t, second.OK)
	assert.Equal(t, entries[5].Hash, second.LastHash)
}

func TestVerifyBatch_ResumedBatchDetectsTamperingInLaterPage(t *testing.T) {
	entries := buildChain(t, 6)
	entries[4].Action = "TAMPERED"

	first := VerifyBatch(entries[:3], nil)
	require.True(t, first.OK)

	second := VerifyBatch(entries[3:], first.LastHash)
	require.False(t, second.OK)
	assert.Equal(t, entries[4].ID, *second.FailedEntryID)
}
