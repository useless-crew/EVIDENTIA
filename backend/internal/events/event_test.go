package events

import "testing"

func TestScopeKey_MatchesEventScopeKey(t *testing.T) {
	e := Event{ResourceType: "case", ResourceID: "abc-123"}
	if got, want := e.ScopeKey(), ScopeKey("case", "abc-123"); got != want {
		t.Fatalf("Event.ScopeKey() = %q, ScopeKey() = %q; must match so a subscriber authorized via the free function is registered under the exact key dispatch computes from a real Event", got, want)
	}
}

func TestScopeKey_DifferentResourceTypesNeverCollide(t *testing.T) {
	a := ScopeKey("case", "1")
	b := ScopeKey("document", "1")
	if a == b {
		t.Fatalf("ScopeKey(%q) must differ by resource type even with the same resource id", a)
	}
}
