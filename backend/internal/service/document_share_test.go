package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"

	"evidentia/backend/db/generated"
	"evidentia/backend/internal/models"
)

func TestToShareSummary_EffectiveStatus(t *testing.T) {
	base := generated.DocumentShare{
		ID:               uuid.New(),
		DocumentID:       uuid.New(),
		SharedWithUserID: uuid.New(),
		CreatedByUserID:  uuid.New(),
		Permission:       models.SharePermissionView,
		CreatedAt:        time.Now(),
	}

	t.Run("active, non-expiring", func(t *testing.T) {
		s := base
		s.Status = models.ShareStatusActive
		s.ExpiresAt = pgtype.Timestamptz{}
		got := toShareSummary(s)
		assert.Equal(t, models.ShareEffectiveStatusActive, got.EffectiveStatus)
	})

	t.Run("active, expires in the future", func(t *testing.T) {
		s := base
		s.Status = models.ShareStatusActive
		s.ExpiresAt = pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}
		got := toShareSummary(s)
		assert.Equal(t, models.ShareEffectiveStatusActive, got.EffectiveStatus)
	})

	t.Run("active, but expiry has passed", func(t *testing.T) {
		s := base
		s.Status = models.ShareStatusActive
		s.ExpiresAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}
		got := toShareSummary(s)
		assert.Equal(t, models.ShareEffectiveStatusExpired, got.EffectiveStatus)
	})

	t.Run("revoked takes precedence regardless of expiry", func(t *testing.T) {
		s := base
		s.Status = models.ShareStatusRevoked
		revokedBy := uuid.New()
		s.RevokedByUserID = &revokedBy
		s.RevokedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		got := toShareSummary(s)
		assert.Equal(t, models.ShareEffectiveStatusRevoked, got.EffectiveStatus)
	})
}

func TestSharePermissionsValidSet(t *testing.T) {
	assert.True(t, sharePermissions[models.SharePermissionView])
	assert.True(t, sharePermissions[models.SharePermissionVerify])
	assert.False(t, sharePermissions["EDIT"])
	assert.False(t, sharePermissions["DELETE"])
	assert.False(t, sharePermissions["REDACT"])
	assert.False(t, sharePermissions["RESHARE"])
	assert.False(t, sharePermissions[""])
}
