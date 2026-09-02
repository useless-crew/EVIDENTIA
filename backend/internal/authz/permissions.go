package authz

// PermissionSet is the union of every Action a user's effective role set
// grants, per role_permissions (see Service.loadPermissions) — the correct
// evaluation for a multi-role user (master prompt §15/§31: "union of
// explicitly permitted actions"). A zero-value (nil) PermissionSet has no
// permissions, which is the correct fail-closed default for the missing/
// unresolvable case (master prompt §2), not a bug callers need to
// nil-check around: Has on a nil map is defined and returns false.
type PermissionSet map[Action]struct{}

// Has reports whether a is present in the set.
func (p PermissionSet) Has(a Action) bool {
	_, ok := p[a]
	return ok
}

func (p PermissionSet) add(a Action) {
	p[a] = struct{}{}
}
