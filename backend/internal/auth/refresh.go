package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// refreshTokenBytes is 256 bits of entropy — brute-forcing it directly is
// infeasible, which is exactly why the lookup hash (below) can be a fast,
// non-adaptive SHA-256 rather than bcrypt: unlike a password, there is no
// low-entropy search space to slow down.
const refreshTokenBytes = 32

// GenerateRefreshToken returns a new cryptographically random, URL-safe
// refresh token — the raw value returned to the client and NEVER
// persisted. Store only HashRefreshToken(raw) (see auth_sessions.token_hash).
func GenerateRefreshToken() (string, error) {
	b := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: generate refresh token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashRefreshToken returns the SHA-256 digest of a raw refresh token, as
// stored in auth_sessions.token_hash (BYTEA, 32 bytes — see
// backend/db/migrations/000002_auth_sessions.up.sql). Looking sessions up
// by this hash is a plain equality lookup (backed by a unique index) —
// deliberately not a constant-time comparison in application code, since
// the 256 bits of entropy in the raw token already make timing-based
// recovery infeasible; that protection matters far more for low-entropy
// secrets like passwords.
func HashRefreshToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}
