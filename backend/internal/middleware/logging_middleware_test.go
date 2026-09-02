package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestRequestLogger_LogsRequestFields(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	r := gin.New()
	r.Use(RequestID())
	r.Use(RequestLogger(logger))
	r.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("Authorization", "Bearer top-secret-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	out := buf.String()
	assert.Contains(t, out, `"path":"/ping"`)
	assert.Contains(t, out, `"method":"GET"`)
	assert.Contains(t, out, `"status":200`)
	assert.Contains(t, out, "request_id")
	assert.Contains(t, out, "duration")

	// The logger never reads/echoes headers, so it cannot leak this by
	// construction — this assertion guards against a future regression.
	assert.NotContains(t, out, "top-secret-token")
}

func TestRequestLogger_UsesWarnForClientErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	r := gin.New()
	r.Use(RequestLogger(logger))
	r.GET("/missing", func(c *gin.Context) { c.Status(http.StatusNotFound) })

	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Contains(t, buf.String(), `"level":"WARN"`)
}
