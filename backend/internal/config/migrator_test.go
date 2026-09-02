package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadMigrator_Valid(t *testing.T) {
	t.Setenv("DATABASE_MIGRATOR_USER", "evidentia")
	t.Setenv("DATABASE_MIGRATOR_PASSWORD", "s3cret")
	t.Setenv("DATABASE_NAME", "evidentia")

	cfg, err := LoadMigrator()
	require.NoError(t, err)
	assert.Equal(t, "evidentia", cfg.User)
	assert.Equal(t, "localhost", cfg.Host)
	assert.Equal(t, 5432, cfg.Port)
	assert.Equal(t, "disable", cfg.SSLMode)
}

func TestLoadMigrator_MissingCredentials(t *testing.T) {
	t.Setenv("DATABASE_MIGRATOR_USER", "")
	t.Setenv("DATABASE_MIGRATOR_PASSWORD", "")
	t.Setenv("DATABASE_NAME", "")

	_, err := LoadMigrator()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_MIGRATOR_USER")
	assert.Contains(t, err.Error(), "DATABASE_MIGRATOR_PASSWORD")
	assert.Contains(t, err.Error(), "DATABASE_NAME")
}

func TestMigratorConfig_DSNEscapesCredentials(t *testing.T) {
	m := MigratorConfig{
		Host: "db.internal", Port: 5432, User: "migrator",
		Password: "p@ss/word?", Name: "evidentia", SSLMode: "require",
	}
	dsn := m.DSN()
	assert.Contains(t, dsn, "postgres://")
	assert.Contains(t, dsn, "sslmode=require")
	assert.NotContains(t, dsn, "p@ss/word?")
}
