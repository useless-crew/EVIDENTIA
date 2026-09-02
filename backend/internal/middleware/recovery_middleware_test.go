package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecovery_ConvertsPanicToSafeJSONError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	r := gin.New()
	r.Use(RequestID())
	r.Use(Recovery(logger))
	r.GET("/boom", func(c *gin.Context) {
		panic("something exploded: db password is hunter2")
	})

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	rec := httptest.NewRecorder()

	require.NotPanics(t, func() { r.ServeHTTP(rec, req) })

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "hunter2")
	assert.Contains(t, rec.Body.String(), "INTERNAL_ERROR")
	assert.Contains(t, rec.Body.String(), "An unexpected error occurred")

	// The panic detail belongs in the server-side log, not the client body.
	assert.Contains(t, logBuf.String(), "hunter2")
}

func TestRecovery_DoesNothingWithoutPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))

	r := gin.New()
	r.Use(Recovery(logger))
	r.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}
