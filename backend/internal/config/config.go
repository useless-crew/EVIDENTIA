// Package config loads and validates Evidentia's typed application
// configuration from environment variables. Configuration is validated
// eagerly at startup: an invalid or incomplete configuration returns an
// error from Load and the process must not proceed with a partially valid
// configuration state (see master prompt §9 — fail closed).
package config

import (
	"fmt"
	"net/url"
	"time"
)

// Config is the root, strongly typed application configuration.
type Config struct {
	App         AppConfig
	Server      ServerConfig
	CORS        CORSConfig
	Database    DatabaseConfig
	Redis       RedisConfig
	MinIO       MinIOConfig
	Logging     LoggingConfig
	JWT         JWTConfig
	Documents   DocumentsConfig
	Certificate CertificateConfig
}

// AppConfig describes general application identity.
type AppConfig struct {
	Env     string // development | test | staging | production
	Name    string
	Version string
}

// IsProduction reports whether the app is running in the production
// environment, useful for callers that must behave more conservatively
// there (e.g. Gin release mode, stricter CORS).
func (a AppConfig) IsProduction() bool {
	return a.Env == "production"
}

// ServerConfig configures the HTTP server and its timeouts. Every timeout is
// mandatory (non-zero) — the application must never run an HTTP server with
// unbounded timeouts (see master prompt §16).
type ServerConfig struct {
	Host            string
	Port            int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
	// MaxBodyBytes bounds the size of JSON request bodies (auth, cases).
	// Document upload endpoints apply DocumentsConfig.MaxUploadSize
	// instead — see body_limit_middleware.go's doc comment for why the
	// two must not compose.
	MaxBodyBytes int64
}

// Addr returns the host:port pair the server should bind to.
func (s ServerConfig) Addr() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

// CORSConfig configures cross-origin request handling. There is
// deliberately no safe universal default for AllowedOrigins in production —
// see validate() — but development defaults to the Angular dev server.
type CORSConfig struct {
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	AllowCredentials bool
}

// DatabaseConfig configures the PostgreSQL connection pool.
type DatabaseConfig struct {
	Host            string
	Port            int
	User            string
	Password        string
	Name            string
	SSLMode         string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// DSN builds a postgres:// connection string suitable for pgxpool, safely
// escaping credentials that may contain reserved URL characters.
func (d DatabaseConfig) DSN() string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(d.User, d.Password),
		Host:   fmt.Sprintf("%s:%d", d.Host, d.Port),
		Path:   "/" + d.Name,
	}
	q := u.Query()
	q.Set("sslmode", d.SSLMode)
	u.RawQuery = q.Encode()
	return u.String()
}

// RedisConfig configures the Redis client.
type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

// MinIOConfig configures the MinIO / S3-compatible object storage client.
type MinIOConfig struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	UseSSL    bool
	Bucket    string
}

// DocumentsConfig configures document upload behavior (System 6).
// MaxUploadSize is deliberately independent of ServerConfig.MaxBodyBytes
// (see body_limit_middleware.go's doc comment): JSON API bodies and
// multipart evidence uploads need very different limits, and applying
// MaxBodyBytes' small default to an upload route would reject legitimate
// evidence files before the handler ever saw them.
type DocumentsConfig struct {
	MaxUploadSize int64
}

// CertificateConfig configures compliance-certificate signing (System 7).
// SigningKeyPEM is deliberately OPTIONAL and carries no default: it is a
// PEM-encoded PKCS#8 ECDSA private key (see pkg/crypto.ParseECDSAPrivateKeyPEM),
// read verbatim from CERTIFICATE_SIGNING_KEY, and MUST NEVER be logged,
// returned through an API response, or given a baked-in fallback value —
// a hardcoded signing key would let anyone who reads the source forge a
// certificate. When unset, service.NewCertificateService generates a
// fresh, process-lifetime-only key instead (see that constructor's doc
// comment for the consequences of doing so) rather than the application
// refusing to start — certificate signing is an enhancement over the
// certificate's core guarantee (binding to the exact document hash),
// not a prerequisite for it.
type CertificateConfig struct {
	SigningKeyPEM string
}

// LoggingConfig configures structured operational logging.
type LoggingConfig struct {
	Level  string
	Format string
}

// JWTConfig configures access-token signing/validation and password
// hashing cost. Access tokens are signed HS256 (a shared secret) rather
// than RS256: this project has no separate service that needs to verify
// tokens without the signing secret, so asymmetric keys would add key-
// management complexity (distributing/rotating a public key) without a
// corresponding benefit here. The claims/validation logic is otherwise
// signing-method-agnostic — switching to RS256 later would mean changing
// the key type and jwt.SigningMethod used, not the surrounding
// architecture. See docs/SECURITY.md.
type JWTConfig struct {
	Issuer     string
	Audience   string
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	SigningKey string
	BcryptCost int
}

var validSSLModes = map[string]bool{
	"disable":     true,
	"allow":       true,
	"prefer":      true,
	"require":     true,
	"verify-ca":   true,
	"verify-full": true,
}

var validAppEnvs = map[string]bool{
	"development": true,
	"test":        true,
	"staging":     true,
	"production":  true,
}

// Load reads configuration from the process environment, applying safe
// defaults for non-sensitive values and requiring explicit values for
// credentials and resource identifiers (no baked-in "admin"/"password"
// style defaults). It returns a combined error describing every problem
// found so a misconfigured deployment can be fixed in one pass.
func Load() (*Config, error) {
	c := &errCollector{}

	cfg := &Config{
		App: AppConfig{
			Env:     getString("APP_ENV", "development"),
			Name:    getString("APP_NAME", "evidentia-backend"),
			Version: getString("APP_VERSION", "dev"),
		},
		Server: ServerConfig{
			Host:            getString("SERVER_HOST", "0.0.0.0"),
			Port:            getInt(c, "SERVER_PORT", 8080),
			ReadTimeout:     getDuration(c, "SERVER_READ_TIMEOUT", 15*time.Second),
			WriteTimeout:    getDuration(c, "SERVER_WRITE_TIMEOUT", 15*time.Second),
			IdleTimeout:     getDuration(c, "SERVER_IDLE_TIMEOUT", 60*time.Second),
			ShutdownTimeout: getDuration(c, "SERVER_SHUTDOWN_TIMEOUT", 15*time.Second),
			MaxBodyBytes:    int64(getInt(c, "SERVER_MAX_BODY_BYTES", 1<<20)), // 1 MiB
		},
		CORS: CORSConfig{
			AllowedOrigins:   getStringSlice("CORS_ALLOWED_ORIGINS", "http://localhost:4200"),
			AllowedMethods:   getStringSlice("CORS_ALLOWED_METHODS", "GET,POST,PUT,PATCH,DELETE,OPTIONS"),
			AllowedHeaders:   getStringSlice("CORS_ALLOWED_HEADERS", "Origin,Content-Type,Accept,Authorization,X-Request-ID"),
			AllowCredentials: getBool(c, "CORS_ALLOW_CREDENTIALS", false),
		},
		Database: DatabaseConfig{
			Host:            getString("DATABASE_HOST", "localhost"),
			Port:            getInt(c, "DATABASE_PORT", 5432),
			User:            requireString(c, "DATABASE_USER"),
			Password:        requireString(c, "DATABASE_PASSWORD"),
			Name:            requireString(c, "DATABASE_NAME"),
			SSLMode:         getString("DATABASE_SSLMODE", "disable"),
			MaxOpenConns:    getInt(c, "DATABASE_MAX_OPEN_CONNS", 25),
			MaxIdleConns:    getInt(c, "DATABASE_MAX_IDLE_CONNS", 5),
			ConnMaxLifetime: getDuration(c, "DATABASE_CONN_MAX_LIFETIME", 5*time.Minute),
		},
		Redis: RedisConfig{
			Addr:     getString("REDIS_ADDR", "localhost:6379"),
			Password: getString("REDIS_PASSWORD", ""),
			DB:       getInt(c, "REDIS_DB", 0),
		},
		MinIO: MinIOConfig{
			Endpoint:  getString("MINIO_ENDPOINT", "localhost:9000"),
			AccessKey: requireString(c, "MINIO_ACCESS_KEY"),
			SecretKey: requireString(c, "MINIO_SECRET_KEY"),
			UseSSL:    getBool(c, "MINIO_USE_SSL", false),
			Bucket:    requireString(c, "MINIO_BUCKET"),
		},
		Logging: LoggingConfig{
			Level:  getString("LOG_LEVEL", "info"),
			Format: getString("LOG_FORMAT", "json"),
		},
		JWT: JWTConfig{
			Issuer:     getString("JWT_ISSUER", "evidentia-api"),
			Audience:   getString("JWT_AUDIENCE", "evidentia-client"),
			AccessTTL:  getDuration(c, "JWT_ACCESS_TTL", 15*time.Minute),
			RefreshTTL: getDuration(c, "JWT_REFRESH_TTL", 168*time.Hour),
			SigningKey: requireString(c, "JWT_SIGNING_KEY"),
			BcryptCost: getInt(c, "BCRYPT_COST", 12),
		},
		Documents: DocumentsConfig{
			// 50 MiB: a sensible default for a hackathon/demo environment
			// (comfortably covers scanned FIRs, forensic report PDFs, and
			// photo evidence) — not a claim about production evidence-file
			// sizing, which a real deployment should tune via
			// MAX_UPLOAD_SIZE.
			MaxUploadSize: int64(getInt(c, "MAX_UPLOAD_SIZE", 50<<20)),
		},
		Certificate: CertificateConfig{
			// No requireString/default here — see CertificateConfig's doc
			// comment: unset is a valid, intentional configuration (an
			// ephemeral in-memory key is used instead), not an error.
			SigningKeyPEM: getString("CERTIFICATE_SIGNING_KEY", ""),
		},
	}

	validate(c, cfg)

	if err := c.err(); err != nil {
		return nil, err
	}
	return cfg, nil
}
