package config

import (
	"time"

	"evidentia/backend/internal/logger"
)

// validate performs cross-field and range checks that getters alone cannot
// express (e.g. "port must be 1-65535", "SERVER_IDLE_TIMEOUT must be
// positive"). Problems are appended to c rather than returned immediately so
// Load can report every issue in one pass.
func validate(c *errCollector, cfg *Config) {
	if !validAppEnvs[cfg.App.Env] {
		c.add("APP_ENV=%q must be one of development, test, staging, production", cfg.App.Env)
	}
	if cfg.App.Name == "" {
		c.add("APP_NAME must not be empty")
	}

	validatePort(c, "SERVER_PORT", cfg.Server.Port)
	validatePositiveDuration(c, "SERVER_READ_TIMEOUT", cfg.Server.ReadTimeout)
	validatePositiveDuration(c, "SERVER_WRITE_TIMEOUT", cfg.Server.WriteTimeout)
	validatePositiveDuration(c, "SERVER_IDLE_TIMEOUT", cfg.Server.IdleTimeout)
	validatePositiveDuration(c, "SERVER_SHUTDOWN_TIMEOUT", cfg.Server.ShutdownTimeout)
	if cfg.Server.MaxBodyBytes <= 0 {
		c.add("SERVER_MAX_BODY_BYTES must be greater than 0, got %d", cfg.Server.MaxBodyBytes)
	}

	if len(cfg.CORS.AllowedOrigins) == 0 {
		c.add("CORS_ALLOWED_ORIGINS must not be empty")
	}
	hasWildcardOrigin := false
	for _, origin := range cfg.CORS.AllowedOrigins {
		if origin != "*" {
			continue
		}
		hasWildcardOrigin = true
		if cfg.App.IsProduction() {
			c.add("CORS_ALLOWED_ORIGINS must not include \"*\" in production")
		}
	}
	// Unlike the production-only wildcard check above, this one applies in
	// EVERY environment (including development/staging/test): a wildcard
	// origin combined with credentialed requests is not merely risky, it
	// defeats the entire point of "credentials" — middleware.CORS reflects
	// the caller's actual Origin header (with
	// Access-Control-Allow-Credentials: true) back to ANY site whenever
	// this combination is configured, since a literal
	// "Access-Control-Allow-Origin: *" is invalid alongside credentialed
	// requests per the Fetch spec. A staging/dev environment running real
	// (non-synthetic) data with this misconfiguration would be exactly as
	// exposed as production.
	if hasWildcardOrigin && cfg.CORS.AllowCredentials {
		c.add("CORS_ALLOWED_ORIGINS must not include \"*\" when CORS_ALLOW_CREDENTIALS=true, in any environment — this combination reflects any origin's requests with credentials enabled, which is never safe")
	}
	if len(cfg.CORS.AllowedMethods) == 0 {
		c.add("CORS_ALLOWED_METHODS must not be empty")
	}

	validatePort(c, "DATABASE_PORT", cfg.Database.Port)
	if !validSSLModes[cfg.Database.SSLMode] {
		c.add("DATABASE_SSLMODE=%q must be one of disable, allow, prefer, require, verify-ca, verify-full", cfg.Database.SSLMode)
	}
	if cfg.Database.MaxOpenConns <= 0 {
		c.add("DATABASE_MAX_OPEN_CONNS must be greater than 0, got %d", cfg.Database.MaxOpenConns)
	}
	if cfg.Database.MaxIdleConns < 0 {
		c.add("DATABASE_MAX_IDLE_CONNS must not be negative, got %d", cfg.Database.MaxIdleConns)
	}
	if cfg.Database.MaxOpenConns > 0 && cfg.Database.MaxIdleConns > cfg.Database.MaxOpenConns {
		c.add("DATABASE_MAX_IDLE_CONNS (%d) must not exceed DATABASE_MAX_OPEN_CONNS (%d)", cfg.Database.MaxIdleConns, cfg.Database.MaxOpenConns)
	}
	if cfg.Database.ConnMaxLifetime < 0 {
		c.add("DATABASE_CONN_MAX_LIFETIME must not be negative, got %s", cfg.Database.ConnMaxLifetime)
	}

	if cfg.Redis.DB < 0 {
		c.add("REDIS_DB must not be negative, got %d", cfg.Redis.DB)
	}
	if cfg.Redis.Addr == "" {
		c.add("REDIS_ADDR must not be empty")
	}

	if cfg.MinIO.Endpoint == "" {
		c.add("MINIO_ENDPOINT must not be empty")
	}

	if cfg.Documents.MaxUploadSize <= 0 {
		c.add("MAX_UPLOAD_SIZE must be greater than 0, got %d", cfg.Documents.MaxUploadSize)
	}

	if _, err := logger.ParseLevel(cfg.Logging.Level); err != nil {
		c.add("%s", err)
	}
	if err := logger.ValidateFormat(cfg.Logging.Format); err != nil {
		c.add("%s", err)
	}

	if cfg.JWT.Issuer == "" {
		c.add("JWT_ISSUER must not be empty")
	}
	if cfg.JWT.Audience == "" {
		c.add("JWT_AUDIENCE must not be empty")
	}
	if cfg.JWT.AccessTTL <= 0 {
		c.add("JWT_ACCESS_TTL must be greater than 0")
	} else if cfg.JWT.AccessTTL > 24*time.Hour {
		c.add("JWT_ACCESS_TTL=%s is too long for a short-lived access token (max 24h)", cfg.JWT.AccessTTL)
	}
	if cfg.JWT.RefreshTTL <= 0 {
		c.add("JWT_REFRESH_TTL must be greater than 0")
	} else if cfg.JWT.AccessTTL > 0 && cfg.JWT.RefreshTTL <= cfg.JWT.AccessTTL {
		c.add("JWT_REFRESH_TTL (%s) must be greater than JWT_ACCESS_TTL (%s)", cfg.JWT.RefreshTTL, cfg.JWT.AccessTTL)
	}
	if len(cfg.JWT.SigningKey) < 32 {
		c.add("JWT_SIGNING_KEY must be at least 32 characters (got %d) — it is an HMAC secret, not a password", len(cfg.JWT.SigningKey))
	}
	if cfg.JWT.BcryptCost < 10 || cfg.JWT.BcryptCost > 31 {
		c.add("BCRYPT_COST must be between 10 and 31 (got %d) — below 10 is considered too weak for production use", cfg.JWT.BcryptCost)
	}

	if cfg.LoginLimit.IPMax <= 0 {
		c.add("LOGIN_RATE_LIMIT_IP_MAX must be greater than 0, got %d", cfg.LoginLimit.IPMax)
	}
	validatePositiveDuration(c, "LOGIN_RATE_LIMIT_IP_WINDOW", cfg.LoginLimit.IPWindow)
	if cfg.LoginLimit.AccountMax <= 0 {
		c.add("LOGIN_RATE_LIMIT_ACCOUNT_MAX must be greater than 0, got %d", cfg.LoginLimit.AccountMax)
	}
	validatePositiveDuration(c, "LOGIN_RATE_LIMIT_ACCOUNT_WINDOW", cfg.LoginLimit.AccountWindow)

	validateBootstrapAdmin(c, cfg.Bootstrap)
}

// validateBootstrapAdmin requires EVIDENTIA_BOOTSTRAP_ADMIN_{EMAIL,PASSWORD,
// NAME} to be either all set or all unset — a partial group almost
// certainly means a deployment typo (e.g. the password var name misspelled)
// and must fail startup loudly rather than silently skip bootstrapping or
// create a user with a placeholder value.
func validateBootstrapAdmin(c *errCollector, b BootstrapAdminConfig) {
	set := 0
	if b.Email != "" {
		set++
	}
	if b.Password != "" {
		set++
	}
	if b.Name != "" {
		set++
	}
	if set == 0 || set == 3 {
		return
	}
	c.add("EVIDENTIA_BOOTSTRAP_ADMIN_EMAIL, _PASSWORD, and _NAME must be either all set or all left unset")
}

func validatePort(c *errCollector, key string, port int) {
	if port < 1 || port > 65535 {
		c.add("%s must be between 1 and 65535, got %d", key, port)
	}
}

func validatePositiveDuration(c *errCollector, key string, d time.Duration) {
	if d <= 0 {
		c.add("%s must be greater than 0", key)
	}
}
