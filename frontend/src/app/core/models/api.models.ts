// TypeScript types mirroring Evidentia's actual backend JSON contract
// (backend/pkg/response, backend/internal/service/*.go,
// backend/internal/handlers/**). Field names match the backend's JSON tags
// exactly (snake_case) — this file does not translate to camelCase, and
// ApiClientService does not rewrite keys, so a type here is always a
// direct, checkable description of a real response body, not a guess.
//
// Only the fields Evidentia's backend actually returns are declared —
// nothing here is speculative.

/** Every API response is wrapped in this envelope (pkg/response.Envelope). */
export interface ApiEnvelope<T> {
  success: boolean;
  data?: T;
  error?: ApiErrorBody;
}

/** pkg/response.ErrorBody. */
export interface ApiErrorBody {
  code: string;
  message: string;
  request_id?: string;
}

/** internal/utils.Meta — pagination metadata attached to list responses. */
export interface PageMeta {
  page: number;
  page_size: number;
  total: number;
  total_pages: number;
}

/** The fixed role catalog (backend/db/seed/001_reference_data.sql). */
export type Role = 'ADMIN' | 'POLICE' | 'FORENSICS' | 'LAWYER' | 'JUDGE';

/** POST /auth/login and POST /auth/refresh's embedded user object. */
export interface AuthUser {
  id: string;
  email: string;
  first_name: string;
  last_name: string;
  role: Role;
}

/** POST /auth/{login,refresh}'s response data. */
export interface AuthTokens {
  access_token: string;
  refresh_token: string;
  token_type: string;
  expires_in: number;
  user: AuthUser;
}

/** internal/service.CaseSummary. */
export interface CaseSummary {
  id: string;
  case_number: string;
  title: string;
  description?: string;
  status: CaseStatus;
  metadata: Record<string, unknown>;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export type CaseStatus =
  'OPEN' | 'UNDER_INVESTIGATION' | 'SUBMITTED' | 'UNDER_REVIEW' | 'CLOSED' | 'ARCHIVED';

/** internal/service.InvolvedPartySummary. */
export interface InvolvedPartySummary {
  id: string;
  party_type: string;
  display_name: string;
  metadata: Record<string, unknown>;
  created_at: string;
}

/** internal/service.DocumentSummary (as embedded in CaseDetail.documents). */
export interface DocumentSummary {
  id: string;
  case_id: string;
  document_type: DocumentType;
  filename: string;
  description?: string;
  mime_type: string;
  file_size: number;
  sha256_hash: string;
  status: DocumentStatus;
  /** Set only for a REDACTED DERIVATIVE — the document it was produced
   * FROM. Absent for an original upload. Use its presence as
   * "is_derivative" rather than a separate flag. */
  parent_document_id?: string;
  uploaded_by: string;
  uploaded_at: string;
}

export type DocumentType =
  'FIR' | 'FORENSIC_REPORT' | 'PHOTO_EVIDENCE' | 'WITNESS_STATEMENT' | 'OTHER';

export type DocumentStatus = 'ACTIVE' | 'TAMPERED';

/** internal/service.TimelineEvent. */
export interface TimelineEvent {
  type: string;
  timestamp: string;
  summary: string;
  related_id?: string;
}

/** internal/service.CaseRelationship. */
export interface CaseRelationship {
  is_owner: boolean;
  is_member: boolean;
  membership_type?: string;
}

/** internal/service.CaseDetail — CaseSummary plus the four embedded blocks. */
export interface CaseDetail extends CaseSummary {
  involved_parties: InvolvedPartySummary[];
  documents: DocumentSummary[];
  timeline: TimelineEvent[];
  relationship: CaseRelationship;
}

/** internal/service.CaseListResult — GET /cases's response data. */
export interface CaseListResult {
  cases: CaseSummary[];
  meta: PageMeta;
}

/** GET /cases's optional query filters (all optional). */
export interface CaseListFilter {
  status?: CaseStatus;
  case_number?: string;
  title?: string;
  created_by?: string;
  created_from?: string;
  created_to?: string;
  page?: number;
  page_size?: number;
}

/** POST /cases's request body. */
export interface CreateCaseRequest {
  case_number: string;
  title: string;
  description?: string;
  status?: CaseStatus;
  metadata?: Record<string, unknown>;
}

/** PUT /cases/:id's request body — a full replacement, not a patch. */
export interface UpdateCaseRequest {
  title: string;
  description?: string;
  status: CaseStatus;
  metadata?: Record<string, unknown>;
}

/** POST /cases/:id/documents's response data (service.DocumentSummary,
 * standalone — same shape as the embedded one but returned on its own). */
export type UploadDocumentResponse = DocumentSummary;

/** POST /documents/:id/verify's response data (service.VerificationResult). */
export interface VerificationResult {
  document_id: string;
  status: 'VERIFIED' | 'INTEGRITY_FAILURE';
  stored_hash: string;
  computed_hash: string;
  verified_at: string;
}

/** One rectangular region to redact, in the SOURCE image's own pixel
 * coordinate space (internal/service.RedactRegion) — never a rendered/
 * zoomed on-screen coordinate; the caller must convert before sending
 * this (see RedactStudioComponent). page must be 1 — every currently
 * supported redaction format is single-page raster. */
export interface RedactRegion {
  page: 1;
  x: number;
  y: number;
  width: number;
  height: number;
}

/** POST /documents/:id/redact's request body. */
export interface RedactRequest {
  reason: string;
  regions: RedactRegion[];
}

/** POST /documents/:id/redact's response data (service.RedactionSummary).
 * `document` is the newly created DERIVATIVE's own summary — never the
 * source's, and never the raw redacted bytes. The source document's own
 * row/hash/object/certificate are completely unaffected by this call. */
export interface RedactionSummary {
  redaction_id: string;
  source_document_id: string;
  reason: string;
  created_at: string;
  document: DocumentSummary;
}

/** document_shares.permission — VIEW (read + download + certificate
 * read) or VERIFY (VIEW's grants plus document:verify). Never implies
 * redact/reshare/delete. */
export type SharePermission = 'VIEW' | 'VERIFY';

/** Stored share status (document_shares.status). */
export type ShareStatus = 'ACTIVE' | 'REVOKED';

/** Computed, API-facing status (service.ShareSummary.effective_status) —
 * ACTIVE/REVOKED mirror the stored status; EXPIRED is derived from
 * expires_at and never itself persisted. */
export type ShareEffectiveStatus = 'ACTIVE' | 'EXPIRED' | 'REVOKED';

/** POST /documents/:id/share's request body. */
export interface CreateShareRequest {
  user_id: string;
  permission: SharePermission;
  expires_at?: string;
  reason?: string;
}

/** internal/service.ShareSummary — POST .../share and
 * POST .../shares/:shareId/revoke's response data, and one entry of
 * GET .../shares. */
export interface ShareSummary {
  share_id: string;
  document_id: string;
  recipient_user_id: string;
  created_by_user_id: string;
  permission: SharePermission;
  status: ShareStatus;
  effective_status: ShareEffectiveStatus;
  expires_at?: string;
  reason?: string;
  created_at: string;
  revoked_at?: string;
  revoked_by_user_id?: string;
}

/** GET /documents/:id/shares's response data. */
export interface ShareListResult {
  shares: ShareSummary[];
}

/** internal/service.SharedDocumentSummary — one row of
 * GET /shared/documents. */
export interface SharedDocumentSummary {
  share_id: string;
  permission: SharePermission;
  expires_at?: string;
  shared_at: string;
  shared_by_user_id: string;
  document: DocumentSummary;
}

/** GET /shared/documents's response data. */
export interface SharedWithMeResult {
  documents: SharedDocumentSummary[];
  meta: PageMeta;
}

/** internal/service.RecipientCandidate — GET /users/search's per-user
 * shape. Deliberately minimal — no phone/status/timestamps. */
export interface RecipientCandidate {
  id: string;
  first_name: string;
  last_name: string;
  display_name?: string;
  email: string;
  roles: Role[];
}

/** GET /users/search's response data. */
export interface UserSearchResult {
  users: RecipientCandidate[];
}

/** GET /documents/:id/certificate's response data (service.CertificateSummary). */
export interface CertificateSummary {
  id: string;
  document_id: string;
  document_hash: string;
  certificate_version: string;
  signature_algorithm: string;
  signature: string;
  issuer: string;
  generated_by: string;
  generated_at: string;
}

/** Account status (users_status_check in the schema). */
export type UserStatus = 'active' | 'inactive' | 'suspended';

/** internal/service.AdminUserSummary — every /admin/users* and /users/me
 * response's user shape. Never carries a password or password hash: the
 * backend structurally cannot return either (see that Go type's own doc
 * comment). */
export interface AdminUser {
  id: string;
  email: string;
  first_name: string;
  last_name: string;
  display_name?: string;
  phone?: string;
  status: UserStatus;
  roles: Role[];
  created_at: string;
  updated_at: string;
  last_login_at?: string;
}

/** internal/service.UserListResult — GET /admin/users's response data. */
export interface AdminUserListResult {
  users: AdminUser[];
  meta: PageMeta;
}

/** GET /admin/users's optional query filters (all optional). */
export interface AdminUserListFilter {
  role?: Role;
  status?: UserStatus;
  search?: string;
  page?: number;
  page_size?: number;
}

/** POST /admin/users's request body. */
export interface CreateUserRequest {
  email: string;
  password: string;
  first_name: string;
  last_name: string;
  display_name?: string;
  phone?: string;
  role: Role;
  status?: UserStatus;
}

/** PUT /admin/users/:id's request body — a full replacement of every
 * mutable profile field (excludes email/password/role/status, each of
 * which has its own dedicated endpoint below). */
export interface UpdateUserRequest {
  first_name: string;
  last_name: string;
  display_name?: string;
  phone?: string;
}

/** PUT /admin/users/:id/role's request body. */
export interface UpdateUserRoleRequest {
  role: Role;
}

/** PUT /admin/users/:id/status's request body. */
export interface UpdateUserStatusRequest {
  status: UserStatus;
}

/** PUT /admin/users/:id/password's request body. */
export interface ResetPasswordRequest {
  password: string;
}

/** GET /admin/roles's response data (internal/service.RoleCatalogEntry). */
export interface RoleCatalogEntry {
  id: string;
  name: Role;
  description?: string;
}

// ---- System 11: Audit Chain Verification & Integrity Dashboard ----
// See docs/AUDIT_CHAIN.md's "Asynchronous Verification & Integrity
// Dashboard" for the full backend design these types mirror.

/** The complete verification-status vocabulary (internal/audit's
 * VerificationStatus* constants) — QUEUED/RUNNING are in-flight;
 * VERIFIED/INTEGRITY_FAILURE/FAILED are terminal. FAILED is an
 * OPERATIONAL failure (e.g. a database outage), never a cryptographic
 * finding — the two are never interchangeable in the UI either. */
export type VerificationStatus = 'QUEUED' | 'RUNNING' | 'VERIFIED' | 'INTEGRITY_FAILURE' | 'FAILED';

/** POST /audit/verify-chain's response data (internal/service.
 * StartVerificationResult) — always 202: this is an ACCEPTANCE, not a
 * result. If a verification was already QUEUED/RUNNING, this is that
 * same run's id, never a newly created duplicate. */
export interface StartVerificationResponse {
  verification_id: string;
  /** System 12: the underlying Asynq task's traceable id — deterministically
   * `audit:verify_chain:<verification_id>` (see jobs.AuditVerifyChainJobID).
   * Not rendered anywhere today; kept for parity with the backend response
   * and for anyone correlating operational logs with a verification run. */
  job_id: string;
  status: VerificationStatus;
  created_at: string;
}

/** GET /audit/verify-chain/:id's response data, and one element of
 * GET /audit/verifications' list (internal/service.VerificationDetail).
 * The SSE stream (see AuditVerificationService) delivers events shaped
 * identically to this — the frontend never has to reconcile two
 * different response shapes for "the same fact". */
export interface VerificationDetail {
  verification_id: string;
  /** System 12: see StartVerificationResponse.job_id. */
  job_id: string;
  status: VerificationStatus;
  entries_checked: number;
  total_entries?: number;
  progress_percent?: number;
  last_seq_checked?: number;
  failed_entry_id?: string;
  failed_seq?: number;
  /** INTEGRITY_FAILURE: GENESIS_INVALID | PREVIOUS_HASH_MISMATCH |
   * ENTRY_HASH_MISMATCH | CANONICALIZATION_ERROR. FAILED: DATABASE_ERROR
   * | TIMEOUT | STALE_TIMEOUT. Absent for QUEUED/RUNNING/VERIFIED. */
  failure_type?: string;
  /** Safe, human-readable text only — never raw SQL/driver detail. */
  failure_reason?: string;
  requested_by_user_id: string;
  requested_by_role?: string;
  started_at?: string;
  completed_at?: string;
  created_at: string;
  updated_at: string;
}

/** GET /audit/verifications' filter/query params. */
export interface VerificationHistoryFilter {
  status?: VerificationStatus;
  requested_by?: string;
  from?: string;
  to?: string;
  page?: number;
  page_size?: number;
}

/** GET /audit/verifications' response data (internal/service.
 * VerificationListResult). */
export interface VerificationHistoryResult {
  verifications: VerificationDetail[];
  meta: PageMeta;
}

/** GET /audit/integrity's response data (internal/service.
 * IntegritySummary) — the dashboard's at-a-glance card. */
export interface IntegritySummary {
  total_entries: number;
  chain_head_seq?: number;
  chain_head_hash?: string;
  last_verification?: VerificationDetail;
}

/** One decoded Server-Sent Event from GET /audit/verify-chain/:id/events
 * (internal/realtime.VerificationEvent) — a safe, progress-focused SUBSET
 * of VerificationDetail's fields (no requested-by or created/updated
 * timestamps on every tick), but every field it DOES carry uses the
 * exact same name as its VerificationDetail counterpart, so rendering
 * code can read either shape without a translation layer. */
export interface VerificationSseEvent {
  type:
    | 'verification_started'
    | 'verification_progress'
    | 'verification_completed'
    | 'verification_integrity_failure'
    | 'verification_failed';
  verification_id: string;
  status: VerificationStatus;
  entries_checked: number;
  total_entries?: number;
  progress_percent?: number;
  failed_entry_id?: string;
  failure_type?: string;
  failure_reason?: string;
  timestamp: string;
}
