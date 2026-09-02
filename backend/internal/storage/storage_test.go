package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestStorage returns a LocalStorage rooted in a fresh temp directory,
// used here as the fast, hermetic stand-in for the Storage interface —
// exercising the same contract MinIOStorage must satisfy, without
// requiring Docker or network access.
func newTestStorage(t *testing.T) Storage {
	t.Helper()
	s, err := NewLocal(t.TempDir())
	require.NoError(t, err)
	return s
}

func TestStorage_PutGetRoundTrip(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	content := []byte("evidence bytes")
	require.NoError(t, s.Put(ctx, "case-1/doc-1.bin", bytes.NewReader(content), int64(len(content)), "application/octet-stream"))

	r, err := s.Get(ctx, "case-1/doc-1.bin")
	require.NoError(t, err)
	defer r.Close()

	got, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, content, got)
}

func TestStorage_ExistsReflectsPutAndDelete(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	exists, err := s.Exists(ctx, "missing.bin")
	require.NoError(t, err)
	assert.False(t, exists)

	require.NoError(t, s.Put(ctx, "present.bin", bytes.NewReader([]byte("x")), 1, ""))

	exists, err = s.Exists(ctx, "present.bin")
	require.NoError(t, err)
	assert.True(t, exists)

	require.NoError(t, s.Delete(ctx, "present.bin"))

	exists, err = s.Exists(ctx, "present.bin")
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestStorage_GetMissingKeyReturnsErrNotFound(t *testing.T) {
	s := newTestStorage(t)

	_, err := s.Get(context.Background(), "does-not-exist.bin")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestStorage_DeleteIsIdempotent(t *testing.T) {
	s := newTestStorage(t)

	err := s.Delete(context.Background(), "never-existed.bin")
	assert.NoError(t, err)
}

func TestStorage_RejectsPathTraversal(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	_, err := s.Get(ctx, "../outside.bin")
	require.Error(t, err)

	err = s.Put(ctx, "../../etc/passwd", bytes.NewReader([]byte("x")), 1, "")
	require.Error(t, err)
}

func TestStorage_HealthCheck(t *testing.T) {
	s := newTestStorage(t)
	assert.NoError(t, s.HealthCheck(context.Background()))
}
