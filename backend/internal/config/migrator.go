package config

import (
	"fmt"
	"net/url"
)

// MigratorConfig configures the privileged database connection used only
// by the migration runner (cmd/migrate) — never by the running server,
// which connects as the least-privilege evidentia_app role via
// DatabaseConfig instead (see master prompt §41/§61 — migrator role ≠
// runtime application role). It shares DATABASE_HOST/PORT/NAME/SSLMODE
// with DatabaseConfig (same physical database) but uses a distinct,
// separately configured credential pair with real DDL privileges.
type MigratorConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Name     string
	SSLMode  string
}

func (m MigratorConfig) DSN() string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(m.User, m.Password),
		Host:   fmt.Sprintf("%s:%d", m.Host, m.Port),
		Path:   "/" + m.Name,
	}
	q := u.Query()
	q.Set("sslmode", m.SSLMode)
	u.RawQuery = q.Encode()
	return u.String()
}

// LoadMigrator reads only the environment variables the migration runner
// needs — independent of the full application Config, so running a
// migration never requires MinIO/Redis/CORS variables to be set.
func LoadMigrator() (*MigratorConfig, error) {
	c := &errCollector{}

	cfg := &MigratorConfig{
		Host:     getString("DATABASE_HOST", "localhost"),
		Port:     getInt(c, "DATABASE_PORT", 5432),
		User:     requireString(c, "DATABASE_MIGRATOR_USER"),
		Password: requireString(c, "DATABASE_MIGRATOR_PASSWORD"),
		Name:     requireString(c, "DATABASE_NAME"),
		SSLMode:  getString("DATABASE_SSLMODE", "disable"),
	}

	validatePort(c, "DATABASE_PORT", cfg.Port)
	if !validSSLModes[cfg.SSLMode] {
		c.add("DATABASE_SSLMODE=%q must be one of disable, allow, prefer, require, verify-ca, verify-full", cfg.SSLMode)
	}

	if err := c.err(); err != nil {
		return nil, err
	}
	return cfg, nil
}
