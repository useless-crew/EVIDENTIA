package authz

// Decision is a policy evaluation's outcome. Reason is an internal,
// non-sensitive diagnostic identifier (e.g. "permission_denied",
// "not_case_member") for server-side logging/audit only — a handler or
// middleware must never return it to a client verbatim (master prompt
// §21/§30: a client sees only a generic 403, never why).
type Decision struct {
	Allowed bool
	Reason  string
}

func allow(reason string) Decision { return Decision{Allowed: true, Reason: reason} }
func deny(reason string) Decision  { return Decision{Allowed: false, Reason: reason} }
