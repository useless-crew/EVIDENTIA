package events

import (
	"encoding/json"
	"testing"
)

func TestBuildEvent_PopulatesEnvelopeFields(t *testing.T) {
	event, payload, err := buildEvent(TypeShareCreated, ResourceTypeCase, "case-1", ShareEventData{ShareID: "s1", DocumentID: "d1", CaseID: "case-1"})
	if err != nil {
		t.Fatalf("buildEvent: %v", err)
	}
	if event.EventID.String() == "" {
		t.Fatal("event_id must never be empty")
	}
	if event.EventType != TypeShareCreated {
		t.Fatalf("event_type = %q, want %q", event.EventType, TypeShareCreated)
	}
	if event.EventVersion != CurrentEventVersion {
		t.Fatalf("event_version = %d, want %d", event.EventVersion, CurrentEventVersion)
	}
	if event.Timestamp.IsZero() {
		t.Fatal("timestamp must never be zero")
	}
	if event.ResourceType != ResourceTypeCase || event.ResourceID != "case-1" {
		t.Fatalf("resource_type/resource_id = %q/%q, want %q/%q", event.ResourceType, event.ResourceID, ResourceTypeCase, "case-1")
	}

	var decoded Event
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("payload must be valid JSON matching Event: %v", err)
	}
	if decoded.EventID != event.EventID {
		t.Fatal("the wire payload's event_id must match the returned Event's own event_id")
	}

	var data ShareEventData
	if err := json.Unmarshal(decoded.Data, &data); err != nil {
		t.Fatalf("data must decode as the caller's own supplied shape: %v", err)
	}
	if data.ShareID != "s1" {
		t.Fatalf("data.share_id = %q, want %q", data.ShareID, "s1")
	}
}

func TestBuildEvent_UniqueEventIDsAcrossCalls(t *testing.T) {
	e1, _, err := buildEvent(TypeShareCreated, ResourceTypeCase, "case-1", ShareEventData{})
	if err != nil {
		t.Fatalf("buildEvent: %v", err)
	}
	e2, _, err := buildEvent(TypeShareCreated, ResourceTypeCase, "case-1", ShareEventData{})
	if err != nil {
		t.Fatalf("buildEvent: %v", err)
	}
	if e1.EventID == e2.EventID {
		t.Fatal("two separately-built events must never share an event_id")
	}
}

func TestBuildEvent_RejectsUnmarshalableData(t *testing.T) {
	_, _, err := buildEvent(TypeShareCreated, ResourceTypeCase, "case-1", func() {})
	if err == nil {
		t.Fatal("a data value json.Marshal cannot encode (a func) must return an error, never silently publish a malformed event")
	}
}

func TestNoopPublisher_NeverPanics(t *testing.T) {
	var p Publisher = NoopPublisher{}
	p.Publish(nil, TypeShareCreated, ResourceTypeCase, "case-1", ShareEventData{}) //nolint:staticcheck // deliberate nil ctx: NoopPublisher must tolerate anything
}
