package storage

import (
	"context"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"evidentia/backend/internal/config"
)

// MinIOStorage implements Storage against a MinIO / S3-compatible endpoint.
type MinIOStorage struct {
	client *minio.Client
	bucket string
}

// NewMinIO connects to the configured MinIO endpoint and ensures the
// configured bucket exists, creating it if this is the first run against a
// fresh instance (idempotent — safe to call on every startup). A
// connectivity or permission problem here fails application startup
// immediately rather than surfacing on the first document upload.
func NewMinIO(ctx context.Context, cfg config.MinIOConfig) (*MinIOStorage, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: create minio client: %w", err)
	}

	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("storage: check bucket %q: %w", cfg.Bucket, err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("storage: create bucket %q: %w", cfg.Bucket, err)
		}
	}

	return &MinIOStorage{client: client, bucket: cfg.Bucket}, nil
}

func (m *MinIOStorage) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := m.client.PutObject(ctx, m.bucket, key, r, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("storage: put %q: %w", key, err)
	}
	return nil
}

func (m *MinIOStorage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := m.client.GetObject(ctx, m.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("storage: get %q: %w", key, err)
	}

	// minio-go's GetObject is lazy: it never errors until the object is
	// first accessed. Stat forces that check now, so Get fails immediately
	// for a missing key rather than on the caller's first Read.
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		if isNoSuchKey(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: stat %q: %w", key, err)
	}

	return obj, nil
}

func (m *MinIOStorage) Delete(ctx context.Context, key string) error {
	if err := m.client.RemoveObject(ctx, m.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("storage: delete %q: %w", key, err)
	}
	return nil
}

func (m *MinIOStorage) Exists(ctx context.Context, key string) (bool, error) {
	_, err := m.client.StatObject(ctx, m.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if isNoSuchKey(err) {
			return false, nil
		}
		return false, fmt.Errorf("storage: stat %q: %w", key, err)
	}
	return true, nil
}

func (m *MinIOStorage) HealthCheck(ctx context.Context) error {
	exists, err := m.client.BucketExists(ctx, m.bucket)
	if err != nil {
		return fmt.Errorf("storage: health check: %w", err)
	}
	if !exists {
		return fmt.Errorf("storage: health check: bucket %q does not exist", m.bucket)
	}
	return nil
}

func isNoSuchKey(err error) bool {
	resp := minio.ToErrorResponse(err)
	return resp.Code == "NoSuchKey"
}
