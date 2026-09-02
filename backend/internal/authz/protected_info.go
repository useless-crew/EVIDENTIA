package authz

import (
	"encoding/json"

	"evidentia/backend/db/generated"
	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/models"
)

// redactedNotice replaces a witness's identity-revealing fields for a
// caller not authorized to see them — a short, obviously-a-placeholder
// string rather than an empty one, so a future UI cannot mistake
// "redacted" for "genuinely blank".
const redactedNotice = "[REDACTED]"

// CanViewProtectedPartyDetails implements master prompt §10's witness-
// identity boundary: only JUDGE, POLICE (this schema's investigating-
// officer role) and ADMIN may see a witness's identifying details. Every
// other role (FORENSICS, LAWYER) may know a witness exists on the case,
// but never who they are.
func CanViewProtectedPartyDetails(user auth.AuthenticatedUser) bool {
	return hasRole(user, models.RoleAdmin) || hasRole(user, models.RolePolice) || hasRole(user, models.RoleJudge)
}

// SanitizeInvolvedParty returns party with identity-revealing fields
// redacted for a caller not authorized to see them, per
// CanViewProtectedPartyDetails. Only WITNESS-type parties are restricted
// today — case_involved_parties carries no classification column finer
// than party_type to generalize this further (master prompt §10: "do NOT
// redesign the entire document model in System 4").
//
// No handler in the repository today serializes case_involved_parties to
// a client — this function exists so the future system that adds one has
// a ready-made, tested enforcement point instead of reinventing one
// inline, per master prompt §10's explicit instruction to "create the
// authorization policy abstraction" and "add the minimum safe
// enforcement" now, even though the enforcement point (a handler) is not
// implemented yet. Authorization must be applied before serialization —
// callers must call this before writing an involved-party record into any
// HTTP response, never after.
//
// Deferred (documented per master prompt §10/§33): a finer-grained
// classification (e.g. redacting only specific metadata keys, or
// protecting non-witness parties too) would need a schema change owned by
// whichever future system needs it — out of scope for System 4.
func SanitizeInvolvedParty(user auth.AuthenticatedUser, party generated.CaseInvolvedParty) generated.CaseInvolvedParty {
	if party.PartyType != models.PartyTypeWitness || CanViewProtectedPartyDetails(user) {
		return party
	}

	party.DisplayName = redactedNotice
	party.Metadata = json.RawMessage(`{}`)
	return party
}
