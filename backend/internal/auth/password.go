// Package auth implements Evidentia's identity primitives: password
// hashing/verification, JWT access-token issuance/validation, opaque
// refresh-token generation/hashing, and the authenticated-request context
// attached by internal/middleware/auth_middleware.go. It establishes WHO
// is making a request — WHAT they are allowed to do is System 4's
// (RBAC/ABAC) responsibility, not this package's.
package auth

import "golang.org/x/crypto/bcrypt"

// HashPassword bcrypt-hashes password at the given cost. Never call this
// with a hardcoded cost — always thread it through from
// config.JWTConfig.BcryptCost, so a single configuration value governs
// both password verification and any future password (re)hashing.
func HashPassword(password string, cost int) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyPassword reports whether password matches hash. It never logs
// either argument — callers must not either.
func VerifyPassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}
