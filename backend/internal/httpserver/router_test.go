package httpserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/app"
	"evidentia/backend/internal/config"
	"evidentia/backend/internal/utils"
)

// fakeDB, fakeCache, and fakeStorage let this test build a fully wired
// router — middleware, health, readiness, 404/405 — without a real
// PostgreSQL, Redis, or MinIO instance.
type fakeDB struct{ err error }

func (f fakeDB) Pool() *pgxpool.Pool        { return nil }
func (f fakeDB) Ping(context.Context) error { return f.err }
func (f fakeDB) Close()                     {}

type fakeCache struct{ err error }

func (f fakeCache) Client() *redis.Client      { return nil }
func (f fakeCache) Ping(context.Context) error { return f.err }
func (f fakeCache) Close() error               { return nil }

type fakeStorage struct{ err error }

func (f fakeStorage) Put(context.Context, string, io.Reader, int64, string) error { return nil }
func (f fakeStorage) Get(context.Context, string) (io.ReadCloser, error)          { return nil, nil }
func (f fakeStorage) Delete(context.Context, string) error                        { return nil }
func (f fakeStorage) Exists(context.Context, string) (bool, error)                { return false, nil }
func (f fakeStorage) HealthCheck(context.Context) error                           { return f.err }

func testApp(db, cache error, store error) *app.App {
	return &app.App{
		Config: &config.Config{
			App:    config.AppConfig{Env: "test", Name: "evidentia-backend", Version: "test"},
			Server: config.ServerConfig{MaxBodyBytes: 1 << 20},
			CORS: config.CORSConfig{
				AllowedOrigins: []string{"https://app.evidentia.example"},
				AllowedMethods: []string{"GET", "POST"},
				AllowedHeaders: []string{"Content-Type"},
			},
		},
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		DB:      fakeDB{err: db},
		Cache:   fakeCache{err: cache},
		Storage: fakeStorage{err: store},
	}
}

func TestRouter_Health(t *testing.T) {
	r := NewRouter(testApp(nil, nil, nil))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotEmpty(t, rec.Header().Get(utils.RequestIDHeader))
}

func TestRouter_ReadyAllHealthy(t *testing.T) {
	r := NewRouter(testApp(nil, nil, nil))

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRouter_ReadyReportsUnhealthyDependency(t *testing.T) {
	r := NewRouter(testApp(errors.New("db down"), nil, nil))

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestRouter_UnknownRouteReturns404Envelope(t *testing.T) {
	r := NewRouter(testApp(nil, nil, nil))

	req := httptest.NewRequest(http.MethodGet, "/does-not-exist", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), utils.CodeNotFound)
}

func TestRouter_WrongMethodReturns405Envelope(t *testing.T) {
	r := NewRouter(testApp(nil, nil, nil))

	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Contains(t, rec.Body.String(), utils.CodeMethodNotAllowed)
}

func TestRouter_CORSHeadersPresentForAllowedOrigin(t *testing.T) {
	r := NewRouter(testApp(nil, nil, nil))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Origin", "https://app.evidentia.example")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, "https://app.evidentia.example", rec.Header().Get("Access-Control-Allow-Origin"))
}
