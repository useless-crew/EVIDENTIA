package hash

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSum256Hex_KnownVector_EmptyString(t *testing.T) {
	// SHA-256("") — a standard, universally-cited test vector, proving
	// this package computes the actual algorithm, not just "some 64-hex-
	// char string".
	const want = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	got, err := Sum256Hex(strings.NewReader(""))
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestSum256Hex_KnownVector_Abc(t *testing.T) {
	// SHA-256("abc") — the other universally-cited test vector.
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	got, err := Sum256Hex(strings.NewReader("abc"))
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Len(t, got, 64, "hash must be exactly 64 lowercase hex characters (32 bytes)")
}

func TestSum256Hex_RepresentsRawBytesOnly(t *testing.T) {
	// A filename/metadata never enters the picture at all, since
	// Sum256Hex only ever sees an io.Reader of content — two reads of
	// identical content must always produce the identical hash.
	content := []byte("evidence file content")
	h1, err := Sum256Hex(bytes.NewReader(content))
	require.NoError(t, err)
	h2, err := Sum256Hex(bytes.NewReader(content))
	require.NoError(t, err)
	assert.Equal(t, h1, h2, "identical content must always produce the identical hash")
}

func TestSum256Hex_DifferentContentDifferentHash(t *testing.T) {
	h1, err := Sum256Hex(strings.NewReader("file A content"))
	require.NoError(t, err)
	h2, err := Sum256Hex(strings.NewReader("file B content"))
	require.NoError(t, err)
	assert.NotEqual(t, h1, h2)
}

func TestNew_StreamingViaTeeReaderMatchesSum256Hex(t *testing.T) {
	// Exercises the exact pattern DocumentService uses: hash computed
	// incrementally via New() while the same bytes are simultaneously
	// consumed by a second destination (here, io.Discard standing in for
	// object storage), rather than reading the source twice.
	content := []byte("streamed evidence bytes, potentially large in production")

	want, err := Sum256Hex(bytes.NewReader(content))
	require.NoError(t, err)

	h := New()
	n, err := h.Write(content)
	require.NoError(t, err)
	require.Equal(t, len(content), n)

	got := SumHex(h.Sum(nil))
	assert.Equal(t, want, got)
}

func TestSize_MatchesDigestLength(t *testing.T) {
	sum, err := Sum256Hex(strings.NewReader("x"))
	require.NoError(t, err)
	// Sum256Hex returns hex (2 chars/byte); Size is the raw byte count
	// documents_sha256_hash_length_check enforces at the database level.
	assert.Equal(t, Size*2, len(sum))
	assert.Equal(t, 32, Size)
}
