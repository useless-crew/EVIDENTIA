package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateRefreshToken_ProducesHighEntropyUniqueValues(t *testing.T) {
	a, err := GenerateRefreshToken()
	require.NoError(t, err)
	b, err := GenerateRefreshToken()
	require.NoError(t, err)

	assert.NotEmpty(t, a)
	assert.NotEqual(t, a, b, "two generated tokens must never collide")
	// 32 raw bytes, base64url (no padding) -> 43 characters.
	assert.Len(t, a, 43)
}

func TestHashRefreshToken_IsDeterministicAndDistinct(t *testing.T) {
	h1 := HashRefreshToken("token-a")
	h2 := HashRefreshToken("token-a")
	h3 := HashRefreshToken("token-b")

	assert.Equal(t, h1, h2, "hashing the same token twice must be deterministic")
	assert.NotEqual(t, h1, h3)
	assert.Len(t, h1, 32, "must match auth_sessions.token_hash's 32-byte CHECK constraint")
}

func TestHashRefreshToken_NeverEqualsRawToken(t *testing.T) {
	raw, err := GenerateRefreshToken()
	require.NoError(t, err)

	hash := HashRefreshToken(raw)
	assert.NotEqual(t, raw, string(hash))
}
