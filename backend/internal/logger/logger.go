// Package logger builds the application's structured operational logger.
//
// This is operational/application logging only — request lifecycle,
// startup/shutdown, dependency failures. It is NOT the Evidentia audit
// trail; the immutable, hash-chained audit log is a separate system
// (internal/audit) implemented later.
package logger

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// New builds a slog.Logger writing to stdout in the given format ("json" or
// "text") at the given level ("debug", "info", "warn", "error").
func New(level, format string) (*slog.Logger, error) {
	lvl, err := ParseLevel(level)
	if err != nil {
		return nil, err
	}

	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	switch strings.ToLower(format) {
	case "json":
		handler = slog.NewJSONHandler(os.Stdout, opts)
	case "text":
		handler = slog.NewTextHandler(os.Stdout, opts)
	default:
		return nil, fmt.Errorf("logger: unsupported LOG_FORMAT %q (expected \"json\" or \"text\")", format)
	}

	return slog.New(handler), nil
}

// ParseLevel validates and converts a LOG_LEVEL string. Exported so the
// config package can fail fast on an invalid value during startup
// validation, before the logger itself is constructed.
func ParseLevel(level string) (slog.Level, error) {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("logger: unsupported LOG_LEVEL %q (expected debug, info, warn, or error)", level)
	}
}

// ValidateFormat validates a LOG_FORMAT string without constructing a logger.
func ValidateFormat(format string) error {
	switch strings.ToLower(format) {
	case "json", "text":
		return nil
	default:
		return fmt.Errorf("logger: unsupported LOG_FORMAT %q (expected \"json\" or \"text\")", format)
	}
}
