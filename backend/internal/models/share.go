package models

import "evidentia/backend/db/generated"

type DocumentShare = generated.DocumentShare

// DocumentShare.Permission values (document_shares_permission_check).
// See db/migrations/000004_document_sharing.up.sql's table comment for
// why there is no separate DOWNLOAD tier: VIEW already covers it.
const (
	SharePermissionView   = "VIEW"
	SharePermissionVerify = "VERIFY"
)

// DocumentShare.Status values (document_shares_status_check). There is
// deliberately no "EXPIRED" stored status — expiration is a function of
// expires_at evaluated at access time (and in RLS), never a value written
// back to the row. See ShareEffectiveStatus.
const (
	ShareStatusActive  = "ACTIVE"
	ShareStatusRevoked = "REVOKED"
)

// Computed, API-facing share states (never persisted) — the three states
// master prompt §11 describes. A share is ShareEffectiveStatusExpired
// when its stored status is still ACTIVE but expires_at has passed;
// ShareEffectiveStatusActive only when ACTIVE and (unexpired or
// non-expiring); ShareEffectiveStatusRevoked mirrors the stored REVOKED
// status directly.
const (
	ShareEffectiveStatusActive  = "ACTIVE"
	ShareEffectiveStatusExpired = "EXPIRED"
	ShareEffectiveStatusRevoked = "REVOKED"
)
