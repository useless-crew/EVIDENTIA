import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import { AuditListFilter, AuditListResult } from '../models/api.models';
import { ApiClientService } from './api-client.service';

/**
 * System 10's hash-chained audit LEDGER listing — GET /audit only.
 * Deliberately separate from AuditVerificationService (System 11), which
 * owns chain-verification RUNS (POST/GET /audit/verify-chain*,
 * GET /audit/integrity): a ledger entry and a verification run are
 * different resources with different authorization (audit:read vs.
 * audit:verify, the latter ADMIN-only per the seed data) — see
 * AuditLogComponent's two tabs, each backed by exactly one of these two
 * services.
 *
 * Row-level visibility beyond RBAC's audit:read gate is PostgreSQL RLS's
 * job (audit_log_select) — every filter below can only narrow what the
 * caller's own identity already permits, never widen it, so this service
 * does no client-side filtering of what the backend returns.
 */
@Injectable({ providedIn: 'root' })
export class AuditService {
  private readonly api = inject(ApiClientService);

  /** GET /audit — the caller's authorized, filtered, paginated audit
   * trail. FORENSICS has no audit:read permission at all (see the seed
   * role/permission matrix) and gets a 403 here, same as any other
   * unauthorized role attempting any other RBAC-gated endpoint. */
  list(filter: AuditListFilter = {}): Observable<AuditListResult> {
    return this.api.get<AuditListResult>('/audit', { ...filter });
  }
}
