package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/blockchain"
	"evidentia/backend/pkg/response"
)

type fakeDBPinger struct{ err error }
func (f fakeDBPinger) Ping(context.Context) error { return f.err }

type fakeCachePinger struct{ err error }
func (f fakeCachePinger) Ping(context.Context) error { return f.err }

type fakeStorage struct{ err error }
func (f fakeStorage) Put(context.Context, string, any, int64, string) error { return f.err }
func (f fakeStorage) Get(context.Context, string) (any, error) { return nil, f.err }
func (f fakeStorage) Delete(context.Context, string) error { return f.err }
func (f fakeStorage) Exists(context.Context, string) (bool, error) { return false, f.err }
func (f fakeStorage) HealthCheck(context.Context) error { return f.err }

func init() {
	gin.SetMode(gin.TestMode)
}

func TestBlockchainInfo_Disabled(t *testing.T) {
	noop := blockchain.NewNoopService()
	assert.False(t, noop.IsEnabled())
}


func TestWriteServiceError(t *testing.T) {
	r := gin.New()
	r.GET("/test-err", func(c *gin.Context) {
		writeServiceError(c, errors.New("boom"))
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test-err", nil)
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	var env response.Envelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.False(t, env.Success)
}
