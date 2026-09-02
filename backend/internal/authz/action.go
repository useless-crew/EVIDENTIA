// Package authz implements Evidentia's authorization layer: centralized
// RBAC (role -> permission, sourced from the roles/permissions/
// role_permissions tables System 2 already established) composed with
// ABAC (case/document relationship checks) on top of it. It is System 4's
// counterpart to System 3's internal/auth: auth establishes WHO is making
// a request (auth.AuthenticatedUser); authz decides WHAT that identity may
// do (master prompt §5).
//
// Authorization here is deliberately deny-by-default and independent of
// PostgreSQL RLS (backend/db/migrations/000001_init_schema.up.sql): RLS is
// defense-in-depth, not a substitute for this layer, and this layer is not
// a substitute for RLS either — see CanAccessCase/CanAccessDocument for
// how the two are made to reinforce each other rather than one blindly
// trusting the other.
package authz

// Action names a single (resource, verb) operation. Values are exactly the
// permissions.name rows seeded by backend/db/seed/001_reference_data.sql —
// this is the ONE place that vocabulary is mirrored into Go, so a
// permission check can compare against a typed constant instead of a
// hand-typed string, without inventing a second, independent catalog. If
// the seed data's permission names ever change, these constants must
// change with them (and vice versa).
type Action string

const (
	ActionCaseCreate Action = "case:create"
	ActionCaseRead   Action = "case:read"
	ActionCaseUpdate Action = "case:update"

	ActionDocumentUpload   Action = "document:upload"
	ActionDocumentRead     Action = "document:read"
	ActionDocumentDownload Action = "document:download"
	ActionDocumentVerify   Action = "document:verify"
	ActionDocumentRedact   Action = "document:redact"
	ActionDocumentShare    Action = "document:share"

	ActionAuditRead   Action = "audit:read"
	ActionAuditVerify Action = "audit:verify"

	ActionCertificateRead   Action = "certificate:read"
	ActionCertificateCreate Action = "certificate:create"

	ActionUserCreate     Action = "user:create"
	ActionUserUpdate     Action = "user:update"
	ActionUserDeactivate Action = "user:deactivate"
	ActionUserRole       Action = "user:role"
)
