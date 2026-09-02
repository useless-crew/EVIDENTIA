package models

import "evidentia/backend/db/generated"

type Case = generated.Case

// Case.Status values (cases_status_check in the schema).
const (
	CaseStatusOpen               = "OPEN"
	CaseStatusUnderInvestigation = "UNDER_INVESTIGATION"
	CaseStatusSubmitted          = "SUBMITTED"
	CaseStatusUnderReview        = "UNDER_REVIEW"
	CaseStatusClosed             = "CLOSED"
	CaseStatusArchived           = "ARCHIVED"
)
