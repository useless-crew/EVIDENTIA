import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import {
  AdminBlockchainInfo,
  AdminDashboardStats,
  AdminJobsSummary,
  AdminSystemHealth,
} from '../models/api.models';
import { ApiClientService } from './api-client.service';

/**
 * Administration & Infrastructure management client service (System 21).
 * Exposes live metrics, system health diagnostics, background job queue inspection,
 * and blockchain infrastructure status.
 * All endpoints are strictly protected by RBAC ADMIN server-side.
 */
@Injectable({ providedIn: 'root' })
export class AdminService {
  private readonly api = inject(ApiClientService);

  /** GET /admin/dashboard/stats — live aggregate counts. */
  getDashboardStats(): Observable<AdminDashboardStats> {
    return this.api.get<AdminDashboardStats>('/admin/dashboard/stats');
  }

  /** GET /admin/system/health — component connectivity, latencies, and runtime stats. */
  getSystemHealth(): Observable<AdminSystemHealth> {
    return this.api.get<AdminSystemHealth>('/admin/system/health');
  }

  /** GET /admin/jobs — Asynq queue depths, active tasks, retry/failed counts. */
  getJobsSummary(): Observable<AdminJobsSummary> {
    return this.api.get<AdminJobsSummary>('/admin/jobs');
  }

  /** GET /admin/blockchain — Hyperledger Fabric status, channel, and anchor ledger stats. */
  getBlockchainInfo(): Observable<AdminBlockchainInfo> {
    return this.api.get<AdminBlockchainInfo>('/admin/blockchain');
  }
}
