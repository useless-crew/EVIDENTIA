package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testBcryptCost = 4 // bcrypt's minimum — fast enough for tests; production uses BCRYPT_COST (>=10, validated by internal/config).

func TestHashPassword_ProducesVerifiableHash(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple", testBcryptCost)
	require.NoError(t, err)
	assert.NotEmpty(t, hash)
	assert.NotEqual(t, "correct horse battery staple", hash, "the hash must never equal the plaintext")
}

func TestVerifyPassword_AcceptsCorrectPassword(t *testing.T) {
	hash, err := HashPassword("s3cur3-p@ssw0rd", testBcryptCost)
	require.NoError(t, err)

	assert.NoError(t, VerifyPassword(hash, "s3cur3-p@ssw0rd"))
}

func TestVerifyPassword_RejectsIncorrectPassword(t *testing.T) {
	hash, err := HashPassword("s3cur3-p@ssw0rd", testBcryptCost)
	require.NoError(t, err)

	assert.Error(t, VerifyPassword(hash, "wrong-password"))
}

func TestHashPassword_SameInputProducesDifferentHashes(t *testing.T) {
	// bcrypt salts automatically — two hashes of the same password must
	// never be identical, and both must still verify correctly.
	h1, err := HashPassword("identical-input", testBcryptCost)
	require.NoError(t, err)
	h2, err := HashPassword("identical-input", testBcryptCost)
	require.NoError(t, err)

	assert.NotEqual(t, h1, h2)
	assert.NoError(t, VerifyPassword(h1, "identical-input"))
	assert.NoError(t, VerifyPassword(h2, "identical-input"))
}
