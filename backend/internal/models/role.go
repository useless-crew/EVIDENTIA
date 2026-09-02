package models

import "evidentia/backend/db/generated"

type Role = generated.Role
type UserRole = generated.UserRole

// Fixed catalog seeded by backend/db/seed/001_reference_data.sql.
const (
	RoleAdmin     = "ADMIN"
	RolePolice    = "POLICE"
	RoleForensics = "FORENSICS"
	RoleLawyer    = "LAWYER"
	RoleJudge     = "JUDGE"
)
