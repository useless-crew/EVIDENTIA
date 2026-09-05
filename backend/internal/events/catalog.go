package events

// Event type catalog — the ONE, exhaustive vocabulary of EventType values
// this codebase ever publishes, all SCREAMING_SNAKE_CASE (matching
// audit.Event.Action's existing convention: DOCUMENT_UPLOADED,
// CASE_CREATED, AUDIT_CHAIN_VERIFICATION_REQUESTED, ...) — never a second,
// differently-cased convention. A new event type is added here, and
// nowhere else, before any code publishes or consumes it.
//
// Resource types (the OTHER half of a ScopeKey) are also centralized here
// for the same reason: one, consistent, lowercase-with-underscores
// vocabulary (matching audit.Event.ResourceType's own existing
// convention: "document", "case", "audit_verification", ...).
const (
	// --- Audit-chain verification (System 11, refactored onto this
	// package's infrastructure by System 13 — see docs/REALTIME_EVENTS.md).
	// ResourceType: ResourceTypeAuditVerification. Data:
	// AuditVerificationData. Published by internal/service.AuditService.
	TypeAuditVerificationStarted   = "AUDIT_VERIFICATION_STARTED"
	TypeAuditVerificationProgress  = "AUDIT_VERIFICATION_PROGRESS"
	TypeAuditVerificationCompleted = "AUDIT_VERIFICATION_COMPLETED"
	TypeAuditIntegrityFailure      = "AUDIT_INTEGRITY_FAILURE"
	TypeAuditVerificationFailed    = "AUDIT_VERIFICATION_FAILED"

	// --- Document verification (System 7's POST /documents/:id/verify).
	// ResourceType: ResourceTypeCase (scoped to the document's case — see
	// docs/REALTIME_EVENTS.md's "Event Scoping" for why per-case, not
	// per-document, is this system's chosen granularity). Data:
	// DocumentVerificationData. Published by
	// internal/service.DocumentService.VerifyDocument for EITHER outcome
	// (VERIFIED or INTEGRITY_FAILURE — see DocumentVerificationData.Result);
	// there is no separate "started"/"progress" event because this
	// operation is synchronous and completes within the same HTTP request
	// that triggered it.
	TypeDocumentVerificationCompleted = "DOCUMENT_VERIFICATION_COMPLETED"
	// TypeDocumentVerificationFailed is reserved for a genuine OPERATIONAL
	// failure of the verification process itself (as opposed to a
	// completed check that FOUND tampering, which is
	// TypeDocumentVerificationCompleted with Result ==
	// DocumentVerificationResultIntegrityFailure) — no current code path
	// publishes it: DocumentService.VerifyDocument's own storage-failure
	// path returns an HTTP error directly to the SAME request that asked
	// for verification, which already tells the caller everything an
	// asynchronous notification would; there is no OTHER connected client
	// who benefits from being told about a request they never made. Kept
	// here, not deleted, so the constant exists if a future caller
	// legitimately needs it (master prompt's own requested catalog names
	// it explicitly) — never publish it speculatively just to use it.
	TypeDocumentVerificationFailed = "DOCUMENT_VERIFICATION_FAILED"

	// --- Compliance certificate generation (System 7's GET
	// /documents/:id/certificate, generation branch). ResourceType:
	// ResourceTypeCase. Data: CertificateGenerationData. Published by
	// internal/service.CertificateService.generateCertificate on success.
	TypeCertificateGenerationCompleted = "CERTIFICATE_GENERATION_COMPLETED"
	// TypeCertificateGenerationFailed: reserved, same reasoning as
	// TypeDocumentVerificationFailed above — generateCertificate's own
	// failure paths (integrity mismatch, signing error) return an HTTP
	// error to the same request; nothing else to notify.
	TypeCertificateGenerationFailed = "CERTIFICATE_GENERATION_FAILED"

	// --- Document redaction (System 8's POST /documents/:id/redact).
	// ResourceType: ResourceTypeCase. Data: DocumentRedactionData.
	// Published by internal/service.DocumentService.RedactDocument on
	// success.
	TypeDocumentRedactionCompleted = "DOCUMENT_REDACTION_COMPLETED"
	// TypeDocumentRedactionFailed: reserved, same reasoning as the two
	// above.
	TypeDocumentRedactionFailed = "DOCUMENT_REDACTION_FAILED"

	// --- Secure document sharing (System 9). ResourceType:
	// ResourceTypeCase. Data: ShareEventData. Published by
	// internal/service.ShareService.CreateShare/RevokeShare.
	TypeShareCreated = "SHARE_CREATED"
	TypeShareRevoked = "SHARE_REVOKED"

	// --- Admin & user management (System 14). ResourceType:
	// ResourceTypeAdminUsers, always scoped to the fixed singleton ID
	// "global" (see internal/service.adminUsersScopeID's own doc comment —
	// admin user management has no per-case/per-agency instance to scope
	// to; it is one global resource, gated entirely by RBAC user:read).
	// Data: AdminUserEventData. Published by
	// internal/service.UserService.CreateUser/UpdateUser/UpdateRole/
	// UpdateStatus. Activated/Deactivated/Suspended are distinct event
	// types (never one generic "USER_STATUS_CHANGED", which would force
	// every subscriber to decode a from/to pair itself just to learn
	// which direction the change went) — see
	// internal/service.statusChangeEventType.
	TypeUserCreated     = "USER_CREATED"
	TypeUserUpdated     = "USER_UPDATED"
	TypeUserRoleChanged = "USER_ROLE_CHANGED"
	TypeUserActivated   = "USER_ACTIVATED"
	TypeUserDeactivated = "USER_DEACTIVATED"
	TypeUserSuspended   = "USER_SUSPENDED"
)

// Resource types — see this file's own package doc comment.
const (
	ResourceTypeAuditVerification = "audit_verification"
	ResourceTypeCase              = "case"
	ResourceTypeAdminUsers        = "admin_users"
)

// AuditVerificationData is every AUDIT_VERIFICATION_*/AUDIT_INTEGRITY_
// FAILURE event's Data payload — a safe, progress-focused SUBSET of
// internal/service.VerificationDetail's own fields (no requested-by
// identity, no created/updated timestamps on every tick — nothing an
// intermediate progress tick needs), field-for-field matching that
// type's JSON tags so a reconnecting client falling back to GET
// /audit/verify-chain/:id (see docs/AUDIT_CHAIN.md's "SSE reconnection")
// can reuse the same field-access code for both. Never carries audit_log
// metadata, document content, or any secret — only identifiers, counts,
// and a classified failure type/reason.
type AuditVerificationData struct {
	VerificationID string   `json:"verification_id"`
	Status         string   `json:"status"`
	EntriesChecked int64    `json:"entries_checked"`
	TotalEntries   *int64   `json:"total_entries,omitempty"`
	ProgressPct    *float64 `json:"progress_percent,omitempty"`
	FailedEntryID  *string  `json:"failed_entry_id,omitempty"`
	FailureType    string   `json:"failure_type,omitempty"`
	FailureReason  string   `json:"failure_reason,omitempty"`
}

// Document-verification outcome — mirrors
// internal/service.VerificationStatusVerified/IntegrityFailure exactly
// (never a third, differently-spelled value) so a frontend already
// familiar with those strings from the synchronous verify response needs
// no translation.
const (
	DocumentVerificationResultVerified         = "VERIFIED"
	DocumentVerificationResultIntegrityFailure = "INTEGRITY_FAILURE"
)

// DocumentVerificationData is DOCUMENT_VERIFICATION_COMPLETED's Data
// payload. DocumentID/CaseID are included because the event's own
// ResourceType/ResourceID is the CASE (see TypeDocumentVerificationCompleted's
// doc comment) — a subscriber needs DocumentID to know WHICH of the
// case's documents this is about. Never the document's actual bytes,
// filename, or any privacy-sensitive metadata (System 8's own witness/
// redaction-privacy concerns) — only identifiers and the hash-comparison
// outcome, the exact same safety posture
// internal/service.VerificationResult already established for the
// synchronous REST response this event accompanies.
type DocumentVerificationData struct {
	DocumentID string `json:"document_id"`
	CaseID     string `json:"case_id"`
	Result     string `json:"result"`
}

// CertificateGenerationData is CERTIFICATE_GENERATION_COMPLETED's Data
// payload — identifiers and the certificate's own bound hash only, never
// the signature bytes or signing key (already never present on
// generated.ComplianceCertificate's own safe API shape,
// internal/service.CertificateSummary, which this mirrors).
type CertificateGenerationData struct {
	CertificateID string `json:"certificate_id"`
	DocumentID    string `json:"document_id"`
	CaseID        string `json:"case_id"`
	DocumentHash  string `json:"document_hash"`
}

// DocumentRedactionData is DOCUMENT_REDACTION_COMPLETED's Data payload —
// identifiers only, never the redaction reason (may describe sensitive
// case context), region coordinates, or either document's actual
// content/hash-in-full-context beyond what a case member could already
// see via GET /cases/:id's own document list.
type DocumentRedactionData struct {
	SourceDocumentID string `json:"source_document_id"`
	ResultDocumentID string `json:"result_document_id"`
	CaseID           string `json:"case_id"`
}

// ShareEventData is SHARE_CREATED/SHARE_REVOKED's Data payload —
// deliberately omits the recipient's identity and the share's permission
// level: a case member merely watching for "the share list changed" does
// not need to already know who it was shared with or how (that detail is
// exactly what GET /documents/:id/shares — itself authorization-checked
// per master prompt's own "Document Privacy" concern — is for); this
// event is a refetch SIGNAL, not a copy of the share record itself.
type ShareEventData struct {
	ShareID    string `json:"share_id"`
	DocumentID string `json:"document_id"`
	CaseID     string `json:"case_id"`
}

// AdminUserEventData is every USER_CREATED/USER_UPDATED/USER_ROLE_
// CHANGED/USER_ACTIVATED/USER_DEACTIVATED/USER_SUSPENDED event's Data
// payload — the same safe fields internal/service.AdminUserSummary
// already exposes over GET /admin/users, never password, password_hash,
// a token, or any other credential (none of which internal/service.
// UserService ever holds in a form this package could even reach). Roles
// is the user's full CURRENT role set (a single-element list in this
// project's one-role-at-a-time model — see UserService.UpdateRole's own
// doc comment) — a subscriber never needs to separately decode an old/new
// role pair; it already knows to refetch GET /admin/users/:id for the
// full, authoritative detail if it needs one.
type AdminUserEventData struct {
	UserID string   `json:"user_id"`
	Email  string   `json:"email"`
	Roles  []string `json:"roles"`
	Status string   `json:"status"`
}
