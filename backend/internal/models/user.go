package models

import "evidentia/backend/db/generated"

// User is a type alias (not a new struct) to the sqlc-generated row type:
// there is nothing to add or hide, and an alias means db/generated and
// internal/models never drift apart or need manual conversion. The one
// caveat is User.LastLoginAt, which stays pgtype.Timestamptz (see
// backend/sqlc.yaml for why) — read it via its .Time/.Valid fields.
type User = generated.User

// User.Status values (users_status_check in the schema).
const (
	UserStatusActive    = "active"
	UserStatusInactive  = "inactive"
	UserStatusSuspended = "suspended"
)
