package models

import "evidentia/backend/db/generated"

// CaseMember is the foundation of case-level access control — see
// case_members' table comment in the migration. RemovedAt stays
// pgtype.Timestamptz (see backend/sqlc.yaml); check .Valid before .Time.
type CaseMember = generated.CaseMember

// CaseMember.MembershipType values (case_members_membership_type_check).
const (
	MembershipTypeOwner        = "OWNER"
	MembershipTypeInvestigator = "INVESTIGATOR"
	MembershipTypeForensics    = "FORENSICS"
	MembershipTypeLawyer       = "LAWYER"
	MembershipTypeJudge        = "JUDGE"
	MembershipTypeViewer       = "VIEWER"
)
