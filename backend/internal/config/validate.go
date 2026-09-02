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
	for _, origin := range cfg.CORS.AllowedOrigins {
		if origin == "*" && cfg.App.IsProduction() {
			c.add("CORS_ALLOWED_ORIGINS must not include \"*\" in production")
		}
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

	if _, err := logger.ParseLevel(cfg.Logging.Level); err != nil {
		c.add("%s", err)
	}
	if err := logger.ValidateFormat(cfg.Logging.Format); err != nil {
		c.add("%s", err)
	}
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
