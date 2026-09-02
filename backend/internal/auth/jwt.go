package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Claims are Evidentia's JWT access-token claims. Role is a point-in-time
// snapshot captured at issuance — NOT the authorization source of truth.
// Neither System 4's RBAC/ABAC nor this system's own auth middleware (which
// re-resolves the caller's current roles from the database on every
// request) may treat it as authoritative; it exists only so a client can
// display something without an extra round trip. See master prompt §15.
type Claims struct {
	Role string `json:"role,omitempty"`
	jwt.RegisteredClaims
}

// JWTManager issues and validates access tokens. Signing is HS256 (a
// shared secret) — see config.JWTConfig's doc comment for why, and how
// switching to RS256 later would be scoped.
type JWTManager struct {
	signingKey []byte
	issuer     string
	audience   string
	accessTTL  time.Duration
}

func NewJWTManager(signingKey, issuer, audience string, accessTTL time.Duration) *JWTManager {
	return &JWTManager{
		signingKey: []byte(signingKey),
		issuer:     issuer,
		audience:   audience,
		accessTTL:  accessTTL,
	}
}

// AccessTTL exposes the configured access-token lifetime (e.g. for the
// login response's expires_in field) without exposing the signing key.
func (m *JWTManager) AccessTTL() time.Duration {
	return m.accessTTL
}

// CreateAccessToken mints a short-lived access token identifying userID.
// sub is always the user's UUID — never email or display name (master
// prompt §16) — and role is captured as a snapshot (see Claims.Role).
func (m *JWTManager) CreateAccessToken(userID uuid.UUID, role string) (signed string, expiresAt time.Time, err error) {
	now := time.Now().UTC()
	expiresAt = now.Add(m.accessTTL)

	claims := Claims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   userID.String(),
			Audience:  jwt.ClaimStrings{m.audience},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        uuid.NewString(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err = token.SignedString(m.signingKey)
	return signed, expiresAt, err
}

// Validate parses and fully verifies tokenString: signing algorithm
// (HS256 only — "none" and any other algorithm are rejected before the
// signing key is ever consulted), signature, issuer, audience, and
// expiration. It returns the parsed claims only once every check passes.
//
// The returned error is one of golang-jwt's sentinel errors (e.g.
// jwt.ErrTokenExpired, jwt.ErrTokenInvalidIssuer) wrapped with context;
// callers use errors.Is against those sentinels to categorize a failure
// for audit/logging purposes, while the HTTP response stays a single
// generic 401 regardless of which check failed (master prompt §30/§46 —
// never leak which specific validation step failed to the client).
func (m *JWTManager) Validate(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		// Defense in depth alongside WithValidMethods below: refuse to hand
		// back the signing key at all unless the token already claims an
		// HMAC algorithm. jwt.ParseWithClaims calls this keyfunc AFTER
		// WithValidMethods has already rejected "none"/unexpected
		// algorithms, so this check is redundant in the current library
		// version — kept anyway because a keyfunc that trusts t.Method
		// unconditionally is a well-known JWT vulnerability class, and this
		// makes the invariant explicit at the one place a key is released.
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("auth: unexpected signing method %v", t.Header["alg"])
		}
		return m.signingKey, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Name}),
		jwt.WithIssuer(m.issuer),
		jwt.WithAudience(m.audience),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, jwt.ErrTokenSignatureInvalid
	}
	if claims.Subject == "" {
		return nil, jwt.ErrTokenInvalidSubject
	}
	return claims, nil
}
