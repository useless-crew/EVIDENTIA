// Package crypto provides the minimal cryptographic primitives Evidentia's
// compliance-certificate abstraction needs (System 7): generating/parsing
// an ECDSA signing key and signing/verifying a payload with it. It
// deliberately does not implement AES-256 encryption at rest (a.go remains
// a TODO — no System through 7 needs it) or RSA signing — ECDSA P-256 is
// the smallest production-quality signing primitive sufficient for
// compliance certificates, per TECH_STACK.md's "RSA/ECDSA reserved for a
// future digital-signature module" now being drawn on by exactly the
// system that needs it.
package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
)

// GenerateECDSAKey creates a fresh P-256 key pair. Used only when no
// persistent signing key is configured (see
// config.CertificateConfig.SigningKeyPEM) — e.g. local development or a
// test run. A key generated this way exists in process memory only: it is
// never logged, persisted, or returned through any API response, and
// certificates signed with it cannot be verified after a process restart
// (a different ephemeral key would then be in use). Production deployments
// that need certificates verifiable across restarts must configure a
// persistent key via CERTIFICATE_SIGNING_KEY.
func GenerateECDSAKey() (*ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("crypto: generate ECDSA key: %w", err)
	}
	return key, nil
}

// ParseECDSAPrivateKeyPEM decodes a PEM-encoded PKCS#8 ECDSA private key —
// the format produced by, e.g.:
//
//	openssl ecparam -genkey -name prime256v1 -noout | openssl pkcs8 -topk8 -nocrypt
//
// The raw PEM text is the caller's configured secret (CERTIFICATE_SIGNING_KEY)
// — it must never be logged, and this function never echoes it back in an
// error message.
func ParseECDSAPrivateKeyPEM(pemData []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, errors.New("crypto: no PEM block found in ECDSA signing key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("crypto: parse PKCS8 ECDSA private key: %w", err)
	}
	ecKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("crypto: configured signing key is not an ECDSA private key")
	}
	return ecKey, nil
}

// SignECDSA signs the SHA-256 digest of payload with priv, returning an
// ASN.1 DER-encoded signature (crypto/ecdsa's standard wire format — the
// same one (crypto/ecdsa).VerifyASN1 expects). payload must already be a
// canonical, deterministic byte representation of whatever is being
// signed (e.g. a fixed-field-order certificate payload) — this function
// signs exactly the bytes given, nothing more.
func SignECDSA(priv *ecdsa.PrivateKey, payload []byte) ([]byte, error) {
	digest := sha256.Sum256(payload)
	sig, err := ecdsa.SignASN1(rand.Reader, priv, digest[:])
	if err != nil {
		return nil, fmt.Errorf("crypto: sign payload: %w", err)
	}
	return sig, nil
}

// VerifyECDSA reports whether sig is a valid ASN.1 DER-encoded ECDSA
// signature over payload's SHA-256 digest under pub. A false result means
// either the payload was altered after signing or the signature does not
// correspond to this key — this function does not distinguish which.
func VerifyECDSA(pub *ecdsa.PublicKey, payload, sig []byte) bool {
	digest := sha256.Sum256(payload)
	return ecdsa.VerifyASN1(pub, digest[:], sig)
}
