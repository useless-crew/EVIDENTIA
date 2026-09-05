package audit

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func baseEntry() Entry {
	uid := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	rid := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	cid := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	return Entry{
		ID:           uuid.MustParse("44444444-4444-4444-4444-444444444444"),
		Timestamp:    time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
		UserID:       &uid,
		Role:         "POLICE",
		Action:       "DOCUMENT_UPLOADED",
		ResourceType: "document",
		ResourceID:   &rid,
		CaseID:       &cid,
		Metadata:     json.RawMessage(`{"filename":"a.txt"}`),
		PrevHash:     nil,
	}
}

func TestComputeEntryHash_Deterministic(t *testing.T) {
	e := baseEntry()
	h1 := ComputeEntryHash(e)
	h2 := ComputeEntryHash(e)
	assert.Equal(t, h1, h2)
	assert.Len(t, h1, EntryHashSize)
}

func TestComputeEntryHash_ChangesWithEachField(t *testing.T) {
	base := ComputeEntryHash(baseEntry())

	t.Run("action", func(t *testing.T) {
		e := baseEntry()
		e.Action = "DOCUMENT_DOWNLOADED"
		assert.NotEqual(t, base, ComputeEntryHash(e))
	})

	t.Run("user_id", func(t *testing.T) {
		e := baseEntry()
		other := uuid.MustParse("99999999-9999-9999-9999-999999999999")
		e.UserID = &other
		assert.NotEqual(t, base, ComputeEntryHash(e))
	})

	t.Run("user_id nil vs set", func(t *testing.T) {
		e := baseEntry()
		e.UserID = nil
		assert.NotEqual(t, base, ComputeEntryHash(e))
	})

	t.Run("timestamp", func(t *testing.T) {
		e := baseEntry()
		e.Timestamp = e.Timestamp.Add(time.Second)
		assert.NotEqual(t, base, ComputeEntryHash(e))
	})

	t.Run("role", func(t *testing.T) {
		e := baseEntry()
		e.Role = "ADMIN"
		assert.NotEqual(t, base, ComputeEntryHash(e))
	})

	t.Run("resource_type", func(t *testing.T) {
		e := baseEntry()
		e.ResourceType = "case"
		assert.NotEqual(t, base, ComputeEntryHash(e))
	})

	t.Run("resource_id", func(t *testing.T) {
		e := baseEntry()
		other := uuid.MustParse("88888888-8888-8888-8888-888888888888")
		e.ResourceID = &other
		assert.NotEqual(t, base, ComputeEntryHash(e))
	})

	t.Run("case_id", func(t *testing.T) {
		e := baseEntry()
		e.CaseID = nil
		assert.NotEqual(t, base, ComputeEntryHash(e))
	})

	t.Run("metadata", func(t *testing.T) {
		e := baseEntry()
		e.Metadata = json.RawMessage(`{"filename":"b.txt"}`)
		assert.NotEqual(t, base, ComputeEntryHash(e))
	})

	t.Run("prev_hash", func(t *testing.T) {
		e := baseEntry()
		e.PrevHash = []byte("01234567890123456789012345678901")[:32]
		assert.NotEqual(t, base, ComputeEntryHash(e))
	})

	t.Run("id", func(t *testing.T) {
		e := baseEntry()
		e.ID = uuid.New()
		assert.NotEqual(t, base, ComputeEntryHash(e))
	})
}

func TestComputeEntryHash_GenesisDistinctFromRealPrevHash(t *testing.T) {
	genesis := baseEntry()
	genesis.PrevHash = nil

	// A prev_hash that happens to be all zero bytes must NOT hash
	// identically to "no prev_hash at all" (genesis) — the genesis
	// marker is a distinct literal token, not merely "empty bytes".
	zeroHash := baseEntry()
	zeroHash.PrevHash = make([]byte, EntryHashSize)

	assert.NotEqual(t, ComputeEntryHash(genesis), ComputeEntryHash(zeroHash))
}

func TestCanonicalizeMetadata(t *testing.T) {
	t.Run("empty/nil input canonicalizes to {}", func(t *testing.T) {
		got, err := CanonicalizeMetadata(nil)
		require.NoError(t, err)
		assert.Equal(t, json.RawMessage("{}"), got)

		got, err = CanonicalizeMetadata(json.RawMessage(""))
		require.NoError(t, err)
		assert.Equal(t, json.RawMessage("{}"), got)
	})

	t.Run("null literal canonicalizes to {}", func(t *testing.T) {
		got, err := CanonicalizeMetadata(json.RawMessage("null"))
		require.NoError(t, err)
		assert.Equal(t, json.RawMessage("{}"), got)
	})

	t.Run("key order does not affect canonical output", func(t *testing.T) {
		a, err := CanonicalizeMetadata(json.RawMessage(`{"b":1,"a":2}`))
		require.NoError(t, err)
		b, err := CanonicalizeMetadata(json.RawMessage(`{"a":2,"b":1}`))
		require.NoError(t, err)
		assert.Equal(t, a, b)
	})

	t.Run("whitespace does not affect canonical output", func(t *testing.T) {
		a, err := CanonicalizeMetadata(json.RawMessage(`{"a":1}`))
		require.NoError(t, err)
		b, err := CanonicalizeMetadata(json.RawMessage(`  {  "a" : 1  }  `))
		require.NoError(t, err)
		assert.Equal(t, a, b)
	})

	t.Run("canonicalizing an already-canonical value is idempotent", func(t *testing.T) {
		once, err := CanonicalizeMetadata(json.RawMessage(`{"z":1,"a":2}`))
		require.NoError(t, err)
		twice, err := CanonicalizeMetadata(once)
		require.NoError(t, err)
		assert.Equal(t, once, twice)
	})

	t.Run("nested objects are also canonicalized", func(t *testing.T) {
		a, err := CanonicalizeMetadata(json.RawMessage(`{"outer":{"b":1,"a":2}}`))
		require.NoError(t, err)
		b, err := CanonicalizeMetadata(json.RawMessage(`{"outer":{"a":2,"b":1}}`))
		require.NoError(t, err)
		assert.Equal(t, a, b)
	})

	t.Run("invalid JSON is rejected", func(t *testing.T) {
		_, err := CanonicalizeMetadata(json.RawMessage(`{not json`))
		require.Error(t, err)
	})
}

func TestComputeEntryHash_MetadataFormattingDoesNotAffectHash(t *testing.T) {
	e1 := baseEntry()
	canonical1, err := CanonicalizeMetadata(json.RawMessage(`{"b":1,"a":2}`))
	require.NoError(t, err)
	e1.Metadata = canonical1

	e2 := baseEntry()
	canonical2, err := CanonicalizeMetadata(json.RawMessage(`{"a":2,"b":1}`))
	require.NoError(t, err)
	e2.Metadata = canonical2

	assert.Equal(t, ComputeEntryHash(e1), ComputeEntryHash(e2), "differently-ordered-but-equivalent metadata must hash identically once canonicalized")
}
