package authz

import (
	"testing"

	"evidentia/backend/db/generated"
	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/models"
)

func TestCanViewProtectedPartyDetails(t *testing.T) {
	cases := []struct {
		role string
		want bool
	}{
		{models.RoleAdmin, true},
		{models.RolePolice, true},
		{models.RoleJudge, true},
		{models.RoleLawyer, false},
		{models.RoleForensics, false},
	}
	for _, c := range cases {
		t.Run(c.role, func(t *testing.T) {
			got := CanViewProtectedPartyDetails(auth.AuthenticatedUser{Roles: []string{c.role}})
			if got != c.want {
				t.Fatalf("role %s: CanViewProtectedPartyDetails = %v, want %v", c.role, got, c.want)
			}
		})
	}
}

func TestSanitizeInvolvedParty_RedactsWitnessForUnauthorizedRole(t *testing.T) {
	party := generated.CaseInvolvedParty{
		PartyType:   models.PartyTypeWitness,
		DisplayName: "Jane Doe",
		Metadata:    []byte(`{"phone":"555-1234"}`),
	}

	for _, role := range []string{models.RoleLawyer, models.RoleForensics} {
		sanitized := SanitizeInvolvedParty(auth.AuthenticatedUser{Roles: []string{role}}, party)
		if sanitized.DisplayName == party.DisplayName {
			t.Fatalf("role %s: expected display name to be redacted", role)
		}
		if string(sanitized.Metadata) == string(party.Metadata) {
			t.Fatalf("role %s: expected metadata to be redacted", role)
		}
	}
}

func TestSanitizeInvolvedParty_PassesThroughForAuthorizedRole(t *testing.T) {
	party := generated.CaseInvolvedParty{
		PartyType:   models.PartyTypeWitness,
		DisplayName: "Jane Doe",
		Metadata:    []byte(`{"phone":"555-1234"}`),
	}

	for _, role := range []string{models.RoleAdmin, models.RolePolice, models.RoleJudge} {
		got := SanitizeInvolvedParty(auth.AuthenticatedUser{Roles: []string{role}}, party)
		if got.DisplayName != party.DisplayName {
			t.Fatalf("role %s: expected the real display name, got %q", role, got.DisplayName)
		}
		if string(got.Metadata) != string(party.Metadata) {
			t.Fatalf("role %s: expected the real metadata", role)
		}
	}
}

func TestSanitizeInvolvedParty_DoesNotRedactNonWitnessParties(t *testing.T) {
	for _, pt := range []string{models.PartyTypeVictim, models.PartyTypeSuspect, models.PartyTypeAccused, models.PartyTypeOther} {
		party := generated.CaseInvolvedParty{PartyType: pt, DisplayName: "John Roe"}
		got := SanitizeInvolvedParty(auth.AuthenticatedUser{Roles: []string{models.RoleLawyer}}, party)
		if got.DisplayName != party.DisplayName {
			t.Fatalf("party type %s must not be redacted by this policy, got %q", pt, got.DisplayName)
		}
	}
}
