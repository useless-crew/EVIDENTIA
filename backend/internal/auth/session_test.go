package auth

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestCurrentUser_AbsentWhenNeverSet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	_, ok := CurrentUser(c)
	assert.False(t, ok, "no authenticated user should ever be present unless auth middleware set one")
}

func TestSetAuthenticatedUser_ThenCurrentUserRoundTrips(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	want := AuthenticatedUser{ID: uuid.New(), Email: "officer@example.com", Roles: []string{"POLICE"}}
	SetAuthenticatedUser(c, want)

	got, ok := CurrentUser(c)
	assert.True(t, ok)
	assert.Equal(t, want, got)
}
