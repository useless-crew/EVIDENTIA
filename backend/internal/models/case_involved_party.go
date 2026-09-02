package models

import "evidentia/backend/db/generated"

// CaseInvolvedParty.Metadata is SENSITIVE — see the table comment in the
// migration before adding any query or handler that exposes it.
type CaseInvolvedParty = generated.CaseInvolvedParty

// CaseInvolvedParty.PartyType values (case_involved_parties_party_type_check).
const (
	PartyTypeVictim  = "VICTIM"
	PartyTypeWitness = "WITNESS"
	PartyTypeSuspect = "SUSPECT"
	PartyTypeAccused = "ACCUSED"
	PartyTypeOther   = "OTHER"
)
