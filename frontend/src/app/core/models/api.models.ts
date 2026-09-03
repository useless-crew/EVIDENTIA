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
  | 'OPEN'
  | 'UNDER_INVESTIGATION'
  | 'SUBMITTED'
  | 'UNDER_REVIEW'
  | 'CLOSED'
  | 'ARCHIVED';

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
  uploaded_by: string;
  uploaded_at: string;
}

export type DocumentType =
  | 'FIR'
  | 'FORENSIC_REPORT'
  | 'PHOTO_EVIDENCE'
  | 'WITNESS_STATEMENT'
  | 'OTHER';

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
