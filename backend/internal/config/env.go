package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// errCollector accumulates configuration problems so startup reports every
// misconfigured value at once instead of forcing a fix-one-rerun loop.
type errCollector struct {
	errs []string
}

func (c *errCollector) add(format string, args ...any) {
	c.errs = append(c.errs, fmt.Sprintf(format, args...))
}

func (c *errCollector) err() error {
	if len(c.errs) == 0 {
		return nil
	}
	return fmt.Errorf("invalid configuration:\n  - %s", strings.Join(c.errs, "\n  - "))
}

func getString(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

// requireString reads a required, non-empty environment variable. Used for
// credentials and identifiers that must never fall back to a baked-in
// default (see TECH_STACK.md / master prompt §8: never default credentials).
func requireString(c *errCollector, key string) string {
	v := os.Getenv(key)
	if strings.TrimSpace(v) == "" {
		c.add("%s is required and must not be empty", key)
	}
	return v
}

func getInt(c *errCollector, key string, def int) int {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		c.add("%s=%q is not a valid integer", key, raw)
		return def
	}
	return v
}

func getBool(c *errCollector, key string, def bool) bool {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	v, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		c.add("%s=%q is not a valid boolean", key, raw)
		return def
	}
	return v
}

// getStringSlice parses a comma-separated environment variable into a
// trimmed, non-empty slice of values.
func getStringSlice(key, defCSV string) []string {
	raw := getString(key, defCSV)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func getDuration(c *errCollector, key string, def time.Duration) time.Duration {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	v, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		c.add("%s=%q is not a valid duration (e.g. \"15s\", \"5m\")", key, raw)
		return def
	}
	return v
}
