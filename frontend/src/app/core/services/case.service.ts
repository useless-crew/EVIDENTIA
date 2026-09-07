import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import {
  CaseDetail,
  CaseListFilter,
  CaseListResult,
  CaseMemberSummary,
  AddCaseMemberRequest,
  CreateCaseRequest,
  UpdateCaseRequest,
} from '../models/api.models';
import { ApiClientService } from './api-client.service';

/**
 * System 5's case-management endpoints, exactly as
 * docs/API_ENDPOINTS.md's "Cases" section documents them — role-scoped
 * listing and IDOR-safe detail/update are enforced entirely server-side
 * (PostgreSQL RLS + RBAC/ABAC); this service does no client-side
 * filtering of what the backend returns.
 */
@Injectable({ providedIn: 'root' })
export class CaseService {
  private readonly api = inject(ApiClientService);

  /** GET /cases — role-scoped, filtered, paginated. */
  list(filter: CaseListFilter = {}): Observable<CaseListResult> {
    return this.api.get<CaseListResult>('/cases', { ...filter });
  }

  /** GET /cases/:id — full detail (metadata, involved parties, documents,
   * timeline, relationship). A case that doesn't exist and one the caller
   * has no relationship to are indistinguishable (403) — see that
   * endpoint's own doc comment. */
  get(id: string): Observable<CaseDetail> {
    return this.api.get<CaseDetail>(`/cases/${id}`);
  }

  /** POST /cases. `case_number`/`title` required; `status` defaults to
   * OPEN server-side when omitted. */
  create(request: CreateCaseRequest): Observable<CaseDetail> {
    return this.api.post<CaseDetail>('/cases', request);
  }

  /** PUT /cases/:id — a full replacement of title/description/status/
   * metadata, not a partial patch (see UpdateCaseRequest's own doc). */
  update(id: string, request: UpdateCaseRequest): Observable<CaseDetail> {
    return this.api.put<CaseDetail>(`/cases/${id}`, request);
  }

  /** GET /cases/:id/members — list active members/collaborators for this case. */
  listMembers(caseId: string): Observable<CaseMemberSummary[]> {
    return this.api.get<CaseMemberSummary[]>(`/cases/${caseId}/members`);
  }

  /** POST /cases/:id/members — assign a user to this case. */
  addMember(caseId: string, request: AddCaseMemberRequest): Observable<CaseMemberSummary> {
    return this.api.post<CaseMemberSummary>(`/cases/${caseId}/members`, request);
  }

  /** DELETE /cases/:id/members/:userId — remove a member from this case. */
  removeMember(caseId: string, userId: string): Observable<{ removed: boolean }> {
    return this.api.delete<{ removed: boolean }>(`/cases/${caseId}/members/${userId}`);
  }
}
