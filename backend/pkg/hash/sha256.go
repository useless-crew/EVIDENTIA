// Package hash provides the SHA-256 primitives Evidentia's document
// pipeline depends on for computing a document's integrity hash at
// ingestion (System 6). It deliberately does NOT implement hash
// comparison/verification workflows, tamper detection, or certificate
// generation — those are System 7's job, built on top of the canonical
// digest this package computes and callers persist to
// documents.sha256_hash.
package hash

import (
	"crypto/sha256"
	"encoding/hex"
	stdhash "hash"
	"io"
)

// Size is the length in bytes of a SHA-256 digest — the same value
// db/migrations/000001_init_schema.up.sql's documents_sha256_hash_length_check
// constrains documents.sha256_hash to (octet_length(sha256_hash) = 32).
const Size = sha256.Size

// New returns a fresh SHA-256 hash.Hash for incremental/streaming use —
// e.g. as the destination side of an io.TeeReader alongside a second
// destination (object storage), so a document's bytes are hashed exactly
// once, in the same pass they're streamed to storage, never buffered
// twice or read from disk/network a second time just to hash them.
func New() stdhash.Hash {
	return sha256.New()
}

// SumHex hex-encodes an already-computed digest (e.g. the return value of
// New().Sum(nil)) as a lowercase hex string — the ONLY string
// representation this project uses at the API/JSON boundary. The
// canonical, persisted form is always the raw 32 bytes (BYTEA in
// PostgreSQL); hex-encode only when serializing to JSON/text.
func SumHex(sum []byte) string {
	return hex.EncodeToString(sum)
}

// Sum256Hex streams r to EOF and returns its SHA-256 digest as lowercase
// hex. It represents exactly the bytes read from r — nothing else
// (filename, metadata, and object key are never mixed into the hash).
// Convenience wrapper for callers that only need the hash and have no
// second destination to tee to (see New for the streaming/tee case
// document upload actually uses).
func Sum256Hex(r io.Reader) (string, error) {
	h := New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return SumHex(h.Sum(nil)), nil
}
