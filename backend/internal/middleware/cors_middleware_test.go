package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"evidentia/backend/internal/config"
)

func newTestRouterWithCORS(cfg config.CORSConfig) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORS(cfg))
	r.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func TestCORS_AllowsConfiguredOrigin(t *testing.T) {
	r := newTestRouterWithCORS(config.CORSConfig{
		AllowedOrigins: []string{"https://app.evidentia.example"},
		AllowedMethods: []string{"GET"},
		AllowedHeaders: []string{"Content-Type"},
	})

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("Origin", "https://app.evidentia.example")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, "https://app.evidentia.example", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "Origin", rec.Header().Get("Vary"))
}

func TestCORS_RejectsUnconfiguredOrigin(t *testing.T) {
	r := newTestRouterWithCORS(config.CORSConfig{
		AllowedOrigins: []string{"https://app.evidentia.example"},
		AllowedMethods: []string{"GET"},
	})

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORS_WildcardWithoutCredentialsUsesStar(t *testing.T) {
	r := newTestRouterWithCORS(config.CORSConfig{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET"},
		AllowCredentials: false,
	})

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("Origin", "https://anything.example")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORS_WildcardWithCredentialsEchoesOrigin(t *testing.T) {
	// A browser rejects "Access-Control-Allow-Origin: *" combined with
	// "Access-Control-Allow-Credentials: true" — the origin must be echoed
	// back explicitly instead.
	r := newTestRouterWithCORS(config.CORSConfig{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET"},
		AllowCredentials: true,
	})

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("Origin", "https://anything.example")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, "https://anything.example", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", rec.Header().Get("Access-Control-Allow-Credentials"))
}

func TestCORS_HandlesPreflight(t *testing.T) {
	r := newTestRouterWithCORS(config.CORSConfig{
		AllowedOrigins: []string{"https://app.evidentia.example"},
		AllowedMethods: []string{"GET", "POST"},
		AllowedHeaders: []string{"Content-Type"},
	})

	req := httptest.NewRequest(http.MethodOptions, "/ping", nil)
	req.Header.Set("Origin", "https://app.evidentia.example")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), "POST")
}
