package logger

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_ValidCombinations(t *testing.T) {
	t.Parallel()

	for _, level := range []string{"debug", "info", "warn", "warning", "error", "DEBUG", "Info"} {
		for _, format := range []string{"json", "text", "JSON"} {
			l, err := New(level, format)
			require.NoError(t, err, "level=%s format=%s", level, format)
			assert.NotNil(t, l)
		}
	}
}

func TestNew_InvalidLevel(t *testing.T) {
	t.Parallel()

	_, err := New("verbose", "json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "LOG_LEVEL")
}

func TestNew_InvalidFormat(t *testing.T) {
	t.Parallel()

	_, err := New("info", "xml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "LOG_FORMAT")
}
