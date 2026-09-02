package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testManager() *JWTManager {
	return NewJWTManager("test-signing-key-at-least-32-characters-long", "evidentia-api", "evidentia-client", 15*time.Minute)
}

func TestJWTManager_CreateAndValidateRoundTrip(t *testing.T) {
	m := testManager()
	userID := uuid.New()

	token, expiresAt, err := m.CreateAccessToken(userID, "LAWYER")
	require.NoError(t, err)
	assert.NotEmpty(t, token)
	assert.WithinDuration(t, time.Now().Add(15*time.Minute), expiresAt, 2*time.Second)

	claims, err := m.Validate(token)
	require.NoError(t, err)
	assert.Equal(t, userID.String(), claims.Subject, "sub must be the user UUID")
	assert.Equal(t, "evidentia-api", claims.Issuer)
	assert.Contains(t, claims.Audience, "evidentia-client")
	assert.Equal(t, "LAWYER", claims.Role)
	assert.NotEmpty(t, claims.ID, "jti must be set")
	require.NotNil(t, claims.IssuedAt)
	require.NotNil(t, claims.ExpiresAt)
	require.NotNil(t, claims.NotBefore)
}

func TestJWTManager_RejectsExpiredToken(t *testing.T) {
	m := NewJWTManager("test-signing-key-at-least-32-characters-long", "evidentia-api", "evidentia-client", -1*time.Minute)
	token, _, err := m.CreateAccessToken(uuid.New(), "LAWYER")
	require.NoError(t, err)

	_, err = m.Validate(token)
	require.Error(t, err)
	assert.ErrorIs(t, err, jwt.ErrTokenExpired)
}

func TestJWTManager_RejectsMalformedToken(t *testing.T) {
	m := testManager()

	for _, malformed := range []string{"", "not-a-jwt", "only.two.parts.too.many", "a.b.c"} {
		_, err := m.Validate(malformed)
		assert.Error(t, err, "expected rejection for %q", malformed)
	}
}

func TestJWTManager_RejectsWrongIssuer(t *testing.T) {
	issuer := NewJWTManager("test-signing-key-at-least-32-characters-long", "some-other-issuer", "evidentia-client", 15*time.Minute)
	token, _, err := issuer.CreateAccessToken(uuid.New(), "LAWYER")
	require.NoError(t, err)

	m := testManager()
	_, err = m.Validate(token)
	require.Error(t, err)
	assert.ErrorIs(t, err, jwt.ErrTokenInvalidIssuer)
}

func TestJWTManager_RejectsWrongAudience(t *testing.T) {
	issuer := NewJWTManager("test-signing-key-at-least-32-characters-long", "evidentia-api", "some-other-audience", 15*time.Minute)
	token, _, err := issuer.CreateAccessToken(uuid.New(), "LAWYER")
	require.NoError(t, err)

	m := testManager()
	_, err = m.Validate(token)
	require.Error(t, err)
	assert.ErrorIs(t, err, jwt.ErrTokenInvalidAudience)
}

func TestJWTManager_RejectsInvalidSignature(t *testing.T) {
	signedByOther := NewJWTManager("a-completely-different-signing-key-32bytes!", "evidentia-api", "evidentia-client", 15*time.Minute)
	token, _, err := signedByOther.CreateAccessToken(uuid.New(), "LAWYER")
	require.NoError(t, err)

	m := testManager()
	_, err = m.Validate(token)
	require.Error(t, err)
	assert.ErrorIs(t, err, jwt.ErrTokenSignatureInvalid)
}

func TestJWTManager_RejectsTamperedSignature(t *testing.T) {
	m := testManager()
	token, _, err := m.CreateAccessToken(uuid.New(), "LAWYER")
	require.NoError(t, err)

	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)
	// Flip the last character of the signature segment.
	sig := []rune(parts[2])
	if sig[len(sig)-1] == 'A' {
		sig[len(sig)-1] = 'B'
	} else {
		sig[len(sig)-1] = 'A'
	}
	tampered := parts[0] + "." + parts[1] + "." + string(sig)

	_, err = m.Validate(tampered)
	assert.Error(t, err)
}

// TestJWTManager_RejectsAlgNone is the explicit security test required by
// master prompt §59: a token asserting alg=none must never authenticate,
// even though golang-jwt's own SigningMethodNone requires an "unsafe" magic
// key to sign it in the first place (an intentional footgun-guard in the
// library) — this confirms Validate() also refuses to verify one, in case
// it were ever constructed by another JWT library or crafted by hand.
func TestJWTManager_RejectsAlgNone(t *testing.T) {
	claims := Claims{
		Role: "ADMIN",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "evidentia-api",
			Subject:   uuid.NewString(),
			Audience:  jwt.ClaimStrings{"evidentia-client"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	signed, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	m := testManager()
	_, err = m.Validate(signed)
	require.Error(t, err, "alg=none must never authenticate")
}

// TestJWTManager_RejectsUnexpectedAlgorithm is the second half of §59: a
// token legitimately signed (just with a different algorithm — RS256
// instead of the configured HS256) must also be rejected, not silently
// accepted because *a* valid signature exists.
func TestJWTManager_RejectsUnexpectedAlgorithm(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	claims := Claims{
		Role: "ADMIN",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "evidentia-api",
			Subject:   uuid.NewString(),
			Audience:  jwt.ClaimStrings{"evidentia-client"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(privateKey)
	require.NoError(t, err)

	m := testManager()
	_, err = m.Validate(signed)
	require.Error(t, err, "a token signed with an unexpected algorithm must never authenticate")
}

func TestJWTManager_RejectsMissingSubject(t *testing.T) {
	m := testManager()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Audience:  jwt.ClaimStrings{m.audience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			// Subject deliberately omitted.
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.signingKey)
	require.NoError(t, err)

	_, err = m.Validate(signed)
	assert.Error(t, err)
}
