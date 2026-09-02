package authz

import "testing"

func TestPermissionSet_NilHasIsAlwaysFalse(t *testing.T) {
	var p PermissionSet
	if p.Has(ActionCaseCreate) {
		t.Fatal("a nil PermissionSet must deny every action, not panic or allow")
	}
}

func TestPermissionSet_AddAndHas(t *testing.T) {
	p := make(PermissionSet)
	p.add(ActionCaseRead)

	if !p.Has(ActionCaseRead) {
		t.Fatal("expected ActionCaseRead to be present after add")
	}
	if p.Has(ActionCaseUpdate) {
		t.Fatal("expected ActionCaseUpdate to be absent")
	}
}
