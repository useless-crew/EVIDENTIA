import { Injectable, inject } from '@angular/core';
import { Observable, map } from 'rxjs';
import {
  CreateShareRequest,
  RecipientCandidate,
  ShareListResult,
  ShareSummary,
  SharedWithMeResult,
  UserSearchResult,
} from '../models/api.models';
import { ApiClientService } from './api-client.service';

/**
 * Secure document sharing & access delegation — every call here maps 1:1
 * to a real backend route (docs/API_ENDPOINTS.md's Documents section);
 * nothing in this service fabricates a result or computes an
 * authoritative permission/expiration decision client-side. The backend
 * remains the sole authority on whether a share is valid — this service
 * only shapes requests/responses.
 */
@Injectable({ providedIn: 'root' })
export class ShareService {
  private readonly api = inject(ApiClientService);

  /** POST /documents/:id/share — grants recipient a specific, revocable
   * permission on this exact document. Never transfers ownership; never
   * lets the recipient reshare/redact. */
  create(documentId: string, request: CreateShareRequest): Observable<ShareSummary> {
    return this.api.post<ShareSummary>(`/documents/${documentId}/share`, request);
  }

  /** GET /documents/:id/shares — every share ever created for this
   * document, including revoked/expired ones (see effective_status).
   * Only visible to a caller authorized to manage this document's
   * sharing (document:share) — never to a mere recipient. */
  list(documentId: string): Observable<ShareListResult> {
    return this.api.get<ShareListResult>(`/documents/${documentId}/shares`);
  }

  /** POST /documents/:id/shares/:shareId/revoke — immediate, permanent,
   * server-enforced. The share record itself is preserved for audit
   * history; there is no un-revoke. */
  revoke(documentId: string, shareId: string): Observable<ShareSummary> {
    return this.api.post<ShareSummary>(`/documents/${documentId}/shares/${shareId}/revoke`);
  }

  /** GET /shared/documents — documents currently, actively shared with
   * the caller. */
  sharedWithMe(page = 1, pageSize = 20): Observable<SharedWithMeResult> {
    return this.api.get<SharedWithMeResult>('/shared/documents', { page, page_size: pageSize });
  }

  /** GET /users/search?q=... — the share-recipient picker's only data
   * source. q must be at least 2 characters (enforced server-side too);
   * results are capped, active-users-only, and exclude the caller. */
  searchRecipients(query: string): Observable<RecipientCandidate[]> {
    return this.api.get<UserSearchResult>('/users/search', { q: query }).pipe(map((result) => result.users));
  }
}
