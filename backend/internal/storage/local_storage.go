package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LocalStorage implements Storage against the local filesystem. It exists
// for local development without MinIO and as a fast, hermetic backend for
// tests exercising the Storage contract — it is not selected by
// configuration for production use (MinIO is).
type LocalStorage struct {
	baseDir string
}

// NewLocal creates a LocalStorage rooted at baseDir, creating the directory
// if it does not already exist.
func NewLocal(baseDir string) (*LocalStorage, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("storage: create base dir %q: %w", baseDir, err)
	}
	return &LocalStorage{baseDir: baseDir}, nil
}

func (l *LocalStorage) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	path, err := l.resolve(key)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("storage: create parent dir for %q: %w", key, err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("storage: open %q for write: %w", key, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, r); err != nil {
		return fmt.Errorf("storage: write %q: %w", key, err)
	}
	return nil
}

func (l *LocalStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	path, err := l.resolve(key)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: open %q for read: %w", key, err)
	}
	return f, nil
}

func (l *LocalStorage) Delete(_ context.Context, key string) error {
	path, err := l.resolve(key)
	if err != nil {
		return err
	}

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("storage: delete %q: %w", key, err)
	}
	return nil
}

func (l *LocalStorage) Exists(_ context.Context, key string) (bool, error) {
	path, err := l.resolve(key)
	if err != nil {
		return false, err
	}

	_, err = os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case os.IsNotExist(err):
		return false, nil
	default:
		return false, fmt.Errorf("storage: stat %q: %w", key, err)
	}
}

func (l *LocalStorage) HealthCheck(_ context.Context) error {
	info, err := os.Stat(l.baseDir)
	if err != nil {
		return fmt.Errorf("storage: health check: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("storage: health check: %q is not a directory", l.baseDir)
	}
	return nil
}

// resolve maps a logical key to a path inside baseDir, rejecting anything
// that could escape it (absolute paths, "..", or empty keys).
func (l *LocalStorage) resolve(key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("storage: key must not be empty")
	}
	if filepath.IsAbs(key) || strings.Contains(key, "..") {
		return "", fmt.Errorf("storage: invalid key %q", key)
	}
	return filepath.Join(l.baseDir, filepath.FromSlash(key)), nil
}
