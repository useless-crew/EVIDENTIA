package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/utils"
)

func newTestRouterWithRequestID() (*gin.Engine, *string) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var seen string
	r.Use(RequestID())
	r.GET("/ping", func(c *gin.Context) {
		seen = utils.GetRequestID(c)
		c.Status(http.StatusOK)
	})
	return r, &seen
}

func TestRequestID_GeneratesWhenAbsent(t *testing.T) {
	r, seen := newTestRouterWithRequestID()

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.NotEmpty(t, *seen)
	assert.Equal(t, *seen, rec.Header().Get(utils.RequestIDHeader))
}

func TestRequestID_PreservesValidIncomingID(t *testing.T) {
	r, seen := newTestRouterWithRequestID()

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set(utils.RequestIDHeader, "client-supplied-id-123")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, "client-supplied-id-123", *seen)
	assert.Equal(t, "client-supplied-id-123", rec.Header().Get(utils.RequestIDHeader))
}

func TestRequestID_RejectsOversizedIncomingID(t *testing.T) {
	r, seen := newTestRouterWithRequestID()

	oversized := strings.Repeat("a", maxIncomingRequestIDLen+1)
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set(utils.RequestIDHeader, oversized)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.NotEqual(t, oversized, *seen)
	assert.NotEmpty(t, *seen)
}

func TestRequestID_RejectsInvalidCharacters(t *testing.T) {
	r, seen := newTestRouterWithRequestID()

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set(utils.RequestIDHeader, "not a valid id!\n")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.NotEqual(t, "not a valid id!\n", *seen)
	assert.NotEmpty(t, *seen)
}

func TestGenerateRequestID_LooksLikeUUIDv4(t *testing.T) {
	id := generateRequestID()
	parts := strings.Split(id, "-")
	require.Len(t, parts, 5)
	assert.Equal(t, "4", string(parts[2][0]))
}
