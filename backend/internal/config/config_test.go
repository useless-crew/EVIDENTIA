package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setRequired sets every environment variable Load() treats as mandatory,
// so individual tests can override just the variable(s) they care about.
func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_USER", "evidentia")
	t.Setenv("DATABASE_PASSWORD", "s3cret")
	t.Setenv("DATABASE_NAME", "evidentia")
	t.Setenv("MINIO_ACCESS_KEY", "access-key")
	t.Setenv("MINIO_SECRET_KEY", "secret-key")
	t.Setenv("MINIO_BUCKET", "evidentia-documents")
	t.Setenv("JWT_SIGNING_KEY", "test-signing-key-at-least-32-characters-long")
}

func TestLoad_ValidConfiguration(t *testing.T) {
	setRequired(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("SERVER_PORT", "9090")
	t.Setenv("DATABASE_SSLMODE", "require")

	cfg, err := Load()
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "production", cfg.App.Env)
	assert.True(t, cfg.App.IsProduction())
	assert.Equal(t, 9090, cfg.Server.Port)
	assert.Equal(t, "require", cfg.Database.SSLMode)
	assert.Equal(t, "evidentia", cfg.Database.User)
	assert.Equal(t, "access-key", cfg.MinIO.AccessKey)
}

func TestLoad_DefaultsAppliedWhenUnset(t *testing.T) {
	setRequired(t)

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "development", cfg.App.Env)
	assert.Equal(t, "evidentia-backend", cfg.App.Name)
	assert.Equal(t, "0.0.0.0", cfg.Server.Host)
	assert.Equal(t, 8080, cfg.Server.Port)
	assert.Equal(t, "localhost", cfg.Database.Host)
	assert.Equal(t, 5432, cfg.Database.Port)
	assert.Equal(t, "disable", cfg.Database.SSLMode)
	assert.Equal(t, "localhost:6379", cfg.Redis.Addr)
	assert.Equal(t, "localhost:9000", cfg.MinIO.Endpoint)
	assert.False(t, cfg.MinIO.UseSSL)
	assert.Equal(t, "info", cfg.Logging.Level)
	assert.Equal(t, "json", cfg.Logging.Format)
	assert.Equal(t, []string{"http://localhost:4200"}, cfg.CORS.AllowedOrigins)
	assert.False(t, cfg.CORS.AllowCredentials)
	assert.Equal(t, int64(1<<20), cfg.Server.MaxBodyBytes)
	assert.Equal(t, "evidentia-api", cfg.JWT.Issuer)
	assert.Equal(t, "evidentia-client", cfg.JWT.Audience)
	assert.Equal(t, 15*time.Minute, cfg.JWT.AccessTTL)
	assert.Equal(t, 168*time.Hour, cfg.JWT.RefreshTTL)
	assert.Equal(t, 12, cfg.JWT.BcryptCost)
	assert.Equal(t, int64(50<<20), cfg.Documents.MaxUploadSize)
}

func TestLoad_RejectsNonPositiveMaxUploadSize(t *testing.T) {
	setRequired(t)
	t.Setenv("MAX_UPLOAD_SIZE", "0")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MAX_UPLOAD_SIZE")
}

func TestLoad_CustomMaxUploadSize(t *testing.T) {
	setRequired(t)
	t.Setenv("MAX_UPLOAD_SIZE", "104857600")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, int64(100<<20), cfg.Documents.MaxUploadSize)
}

func TestLoad_RejectsShortJWTSigningKey(t *testing.T) {
	setRequired(t)
	t.Setenv("JWT_SIGNING_KEY", "too-short")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JWT_SIGNING_KEY")
}

func TestLoad_RejectsExcessiveAccessTTL(t *testing.T) {
	setRequired(t)
	t.Setenv("JWT_ACCESS_TTL", "48h")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JWT_ACCESS_TTL")
}

func TestLoad_RejectsRefreshTTLNotLongerThanAccessTTL(t *testing.T) {
	setRequired(t)
	t.Setenv("JWT_ACCESS_TTL", "1h")
	t.Setenv("JWT_REFRESH_TTL", "30m")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JWT_REFRESH_TTL")
}

func TestLoad_RejectsWeakBcryptCost(t *testing.T) {
	setRequired(t)
	t.Setenv("BCRYPT_COST", "4")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "BCRYPT_COST")
}

func TestLoad_RejectsWildcardCORSInProduction(t *testing.T) {
	setRequired(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://app.evidentia.example,*")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CORS_ALLOWED_ORIGINS")
}

func TestLoad_AllowsWildcardCORSInDevelopment(t *testing.T) {
	setRequired(t)
	t.Setenv("APP_ENV", "development")
	t.Setenv("CORS_ALLOWED_ORIGINS", "*")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, []string{"*"}, cfg.CORS.AllowedOrigins)
}

// TestLoad_RejectsWildcardCORSWithCredentials_AnyEnvironment covers System
// 15's hardening of the wildcard-origin check: TestLoad_
// RejectsWildcardCORSInProduction above only exercises production, but
// wildcard-plus-credentials must never pass validation in ANY
// environment, since it reflects any origin's requests with credentials
// enabled regardless of APP_ENV.
func TestLoad_RejectsWildcardCORSWithCredentials_AnyEnvironment(t *testing.T) {
	for _, env := range []string{"development", "test", "staging", "production"} {
		t.Run(env, func(t *testing.T) {
			setRequired(t)
			t.Setenv("APP_ENV", env)
			t.Setenv("CORS_ALLOWED_ORIGINS", "*")
			t.Setenv("CORS_ALLOW_CREDENTIALS", "true")

			_, err := Load()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "CORS_ALLOW_CREDENTIALS")
		})
	}
}

func TestLoad_MissingRequiredCredentials(t *testing.T) {
	setRequired(t)
	// Simulate the credentials never having been set.
	t.Setenv("DATABASE_PASSWORD", "")
	t.Setenv("MINIO_SECRET_KEY", "")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_PASSWORD")
	assert.Contains(t, err.Error(), "MINIO_SECRET_KEY")
}

func TestLoad_NeverDefaultsCredentials(t *testing.T) {
	// Explicitly clear every credential rather than relying on the
	// invoking shell having none set — this test must hold regardless of
	// what the ambient environment (e.g. a CI job, or another test run
	// with -tags=integration) happens to export.
	keys := []string{"DATABASE_USER", "DATABASE_PASSWORD", "DATABASE_NAME", "MINIO_ACCESS_KEY", "MINIO_SECRET_KEY", "MINIO_BUCKET", "JWT_SIGNING_KEY"}
	for _, key := range keys {
		t.Setenv(key, "")
	}

	// Load must fail rather than silently substituting a placeholder like
	// "admin"/"password".
	_, err := Load()
	require.Error(t, err)
	for _, key := range keys {
		assert.Contains(t, err.Error(), key)
	}
}

func TestLoad_InvalidValues(t *testing.T) {
	setRequired(t)
	t.Setenv("SERVER_PORT", "not-a-number")
	t.Setenv("DATABASE_PORT", "70000")
	t.Setenv("DATABASE_SSLMODE", "bogus")
	t.Setenv("APP_ENV", "prod-typo")
	t.Setenv("LOG_LEVEL", "verbose")

	_, err := Load()
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "SERVER_PORT")
	assert.Contains(t, msg, "DATABASE_PORT")
	assert.Contains(t, msg, "DATABASE_SSLMODE")
	assert.Contains(t, msg, "APP_ENV")
	assert.Contains(t, msg, "LOG_LEVEL")
}

func TestLoad_MaxIdleConnsExceedsMaxOpenConns(t *testing.T) {
	setRequired(t)
	t.Setenv("DATABASE_MAX_OPEN_CONNS", "5")
	t.Setenv("DATABASE_MAX_IDLE_CONNS", "10")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_MAX_IDLE_CONNS")
}

func TestDatabaseConfig_DSN(t *testing.T) {
	d := DatabaseConfig{
		Host:     "db.internal",
		Port:     5432,
		User:     "evidentia",
		Password: "p@ss/word?",
		Name:     "evidentia",
		SSLMode:  "require",
	}

	dsn := d.DSN()
	assert.Contains(t, dsn, "postgres://")
	assert.Contains(t, dsn, "db.internal:5432")
	assert.Contains(t, dsn, "/evidentia")
	assert.Contains(t, dsn, "sslmode=require")
	// The password must be percent-encoded, not interpolated raw.
	assert.NotContains(t, dsn, "p@ss/word?")
}

func TestServerConfig_Addr(t *testing.T) {
	s := ServerConfig{Host: "0.0.0.0", Port: 8080}
	assert.Equal(t, "0.0.0.0:8080", s.Addr())
}
