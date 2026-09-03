package crypto

import (
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignVerifyECDSA_RoundTrip(t *testing.T) {
	key, err := GenerateECDSAKey()
	require.NoError(t, err)

	payload := []byte("certificate_id=x\ndocument_id=y\ndocument_hash=z")
	sig, err := SignECDSA(key, payload)
	require.NoError(t, err)
	assert.NotEmpty(t, sig)

	assert.True(t, VerifyECDSA(&key.PublicKey, payload, sig))
}

func TestVerifyECDSA_RejectsModifiedPayload(t *testing.T) {
	key, err := GenerateECDSAKey()
	require.NoError(t, err)

	sig, err := SignECDSA(key, []byte("original payload"))
	require.NoError(t, err)

	assert.False(t, VerifyECDSA(&key.PublicKey, []byte("tampered payload"), sig),
		"a signature must not verify against a payload it wasn't produced for")
}

func TestVerifyECDSA_RejectsModifiedSignature(t *testing.T) {
	key, err := GenerateECDSAKey()
	require.NoError(t, err)

	payload := []byte("payload")
	sig, err := SignECDSA(key, payload)
	require.NoError(t, err)

	corrupted := append([]byte(nil), sig...)
	corrupted[len(corrupted)-1] ^= 0xFF

	assert.False(t, VerifyECDSA(&key.PublicKey, payload, corrupted))
}

func TestVerifyECDSA_RejectsWrongKey(t *testing.T) {
	key1, err := GenerateECDSAKey()
	require.NoError(t, err)
	key2, err := GenerateECDSAKey()
	require.NoError(t, err)

	payload := []byte("payload")
	sig, err := SignECDSA(key1, payload)
	require.NoError(t, err)

	assert.False(t, VerifyECDSA(&key2.PublicKey, payload, sig),
		"a signature must not verify under a different key pair")
}

func TestParseECDSAPrivateKeyPEM_RoundTripsWithGeneratedKey(t *testing.T) {
	key, err := GenerateECDSAKey()
	require.NoError(t, err)

	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	parsed, err := ParseECDSAPrivateKeyPEM(pemBytes)
	require.NoError(t, err)
	assert.Equal(t, key.D, parsed.D, "parsed key must be the exact same private scalar")

	payload := []byte("round trip payload")
	sig, err := SignECDSA(parsed, payload)
	require.NoError(t, err)
	assert.True(t, VerifyECDSA(&key.PublicKey, payload, sig))
}

func TestParseECDSAPrivateKeyPEM_RejectsGarbage(t *testing.T) {
	_, err := ParseECDSAPrivateKeyPEM([]byte("not a pem file"))
	require.Error(t, err)
}

func TestParseECDSAPrivateKeyPEM_RejectsNonECDSAKey(t *testing.T) {
	// An RSA key encoded as PKCS8 PEM must be rejected — the configured
	// CERTIFICATE_SIGNING_KEY must specifically be ECDSA.
	rsaPEM := `-----BEGIN PRIVATE KEY-----
MIIBVQIBADANBgkqhkiG9w0BAQEFAASCAT8wggE7AgEAAkEAv7HH0IhtZ8n9uOd8
BOGE1TZgN6Q3XwGvVj4Q1lQwXqfM7dxdT4TDrJHNQyq1gRz1cZKrOc3M8gk3uJ2R
wJvW1QIDAQABAkAI4Q1lQwXqfM7dxdT4TDrJHNQyq1gRz1cZKrOc3M8gk3uJ2RwJ
vW1QIhAM+e8BOGE1TZgN6Q3XwGvVj4Q1lQwXqfM7dxdT4TDrAiEA6vY1lQwXqfM7
dxdT4TDrJHNQyq1gRz1cZKrOc3M8gk0CIQCe8BOGE1TZgN6Q3XwGvVj4Q1lQwXqf
M7dxdT4TDrJHQIhAM7dxdT4TDrJHNQyq1gRz1cZKrOc3M8gk3uJ2RwJvW1QAiA6
vY1lQwXqfM7dxdT4TDrJHNQyq1gRz1cZKrOc3M8gk0=
-----END PRIVATE KEY-----`
	_, err := ParseECDSAPrivateKeyPEM([]byte(rsaPEM))
	require.Error(t, err, "a malformed/non-ECDSA PEM must be rejected, not silently accepted")
}
