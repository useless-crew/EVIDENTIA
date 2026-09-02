// Package storage defines the object-storage abstraction that later
// document-handling systems will depend on, plus the infrastructure-level
// connectivity for its MinIO-backed implementation. Document business logic
// (hashing, chain of custody, evidence metadata) belongs to those later
// systems — this package only moves bytes in and out of a named key.
package storage

import (
	"context"
	"errors"
	"io"
)

// ErrNotFound is returned by Get and, where applicable, Delete when the
// requested key does not exist. Both implementations in this package map
// their provider-specific "not found" conditions to this sentinel so
// callers can use errors.Is regardless of backend.
var ErrNotFound = errors.New("storage: object not found")

// Storage abstracts an object store so document services can depend on an
// interface rather than a specific provider (MinIO in production, local
// disk for development/tests).
type Storage interface {
	// Put stores size bytes read from r under key, overwriting any existing
	// object at that key.
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error

	// Get returns a reader for the object at key. The caller must Close it.
	// Returns ErrNotFound if key does not exist.
	Get(ctx context.Context, key string) (io.ReadCloser, error)

	// Delete removes the object at key. It is idempotent: deleting a
	// nonexistent key is not an error.
	Delete(ctx context.Context, key string) error

	// Exists reports whether an object exists at key.
	Exists(ctx context.Context, key string) (bool, error)

	// HealthCheck verifies the storage backend is currently reachable and
	// correctly configured (e.g. the configured bucket/directory exists).
	HealthCheck(ctx context.Context) error
}
