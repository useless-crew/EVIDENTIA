import { Injectable, inject, signal } from '@angular/core';
import { Observable } from 'rxjs';
import {
  AuditVerificationEventData,
  IntegritySummary,
  RealtimeEvent,
  StartVerificationResponse,
  VerificationDetail,
  VerificationHistoryFilter,
  VerificationHistoryResult,
} from '../models/api.models';
import { ApiClientService } from './api-client.service';
import { EventStreamService } from './event-stream.service';

/** Terminal — VerificationStatus values that will never change again. */
const TERMINAL_STATUSES = new Set(['VERIFIED', 'INTEGRITY_FAILURE', 'FAILED']);

function isTerminal(status: string | undefined): boolean {
  return !!status && TERMINAL_STATUSES.has(status);
}

/**
 * System 11's audit-chain verification client: REST calls (via the shared
 * ApiClientService — no scattered fetch()/HttpClient use elsewhere) plus a
 * live-progress watcher combining Server-Sent Events with a REST polling
 * backstop. The SSE half is now built on System 13's shared
 * EventStreamService (see that service's own doc comment) rather than a
 * bespoke fetch()/ReadableStream parsing loop of this service's own — the
 * live-watching BEHAVIOR below (poll timer, terminal detection, REST as
 * the source of truth) is unchanged from before that refactor.
 *
 * Why a backstop in addition to SSE: the REST status endpoint, not
 * accumulated SSE events, is always treated as the source of truth for
 * "what is the CURRENT state" (see docs/AUDIT_CHAIN.md's "SSE
 * reconnection") — every poll goes through ApiClientService/HttpClient,
 * which means AuthInterceptor's existing 401-triggers-one-refresh-and-
 * retry logic covers it automatically. The SSE half exists purely so the
 * dashboard updates quickly; if it drops, disconnects, or errors, watch()
 * simply keeps relying on the poll timer already running underneath it —
 * never leaving the UI stuck on stale data purely because a stream
 * connection failed.
 *
 * The backend is authoritative for VERIFIED/INTEGRITY_FAILURE/progress —
 * this service never computes any of those itself; it only relays
 * whatever the server returns.
 */
@Injectable({ providedIn: 'root' })
export class AuditVerificationService {
  private readonly api = inject(ApiClientService);
  private readonly eventStream = inject(EventStreamService);

  /** The verification currently being watched, or null if none. Updated
   * by both the poll timer (a full VerificationDetail) and SSE frames
   * (that event's own AuditVerificationEventData, unwrapped from its
   * RealtimeEvent envelope — see EventStreamService/internal/events) —
   * always server-derived, and the two shapes share every field a
   * template actually reads (status, entries_checked, total_entries,
   * progress_percent, failed_entry_id, failure_type, failure_reason). */
  readonly current = signal<VerificationDetail | AuditVerificationEventData | null>(null);
  readonly sseConnected = signal(false);
  readonly watchError = signal<string | null>(null);

  private watchingId: string | null = null;
  private pollHandle: ReturnType<typeof setInterval> | null = null;
  private stopStream: (() => void) | null = null;

  // ---- REST ----

  getIntegritySummary(): Observable<IntegritySummary> {
    return this.api.get<IntegritySummary>('/audit/integrity');
  }

  startVerification(): Observable<StartVerificationResponse> {
    return this.api.post<StartVerificationResponse>('/audit/verify-chain');
  }

  getVerificationStatus(id: string): Observable<VerificationDetail> {
    return this.api.get<VerificationDetail>(`/audit/verify-chain/${id}`);
  }

  getVerificationHistory(
    filter: VerificationHistoryFilter = {},
  ): Observable<VerificationHistoryResult> {
    return this.api.get<VerificationHistoryResult>('/audit/verifications', { ...filter });
  }

  // ---- Live watching (REST poll backstop + best-effort SSE) ----

  /** Begin watching one verification's live progress. Safe to call again
   * with a different id — stops the previous watch first. */
  watch(verificationId: string): void {
    this.stop();
    this.watchingId = verificationId;
    this.watchError.set(null);
    this.refreshStatus();
    this.pollHandle = setInterval(() => this.refreshStatus(), 4000);
    this.stopStream = this.eventStream.connect(
      `/audit/verify-chain/${verificationId}/events`,
      (event) => this.handleEvent(verificationId, event),
      (connected) => this.sseConnected.set(connected),
    );
  }

  /** Stop watching — closes the SSE connection (if any) and the poll
   * timer. Always safe to call, including when nothing is being watched
   * (component ngOnDestroy/navigation-away). */
  stop(): void {
    this.stopStream?.();
    this.stopStream = null;
    if (this.pollHandle !== null) {
      clearInterval(this.pollHandle);
      this.pollHandle = null;
    }
    this.watchingId = null;
    this.sseConnected.set(false);
  }

  private refreshStatus(): void {
    const id = this.watchingId;
    if (!id) return;
    this.getVerificationStatus(id).subscribe({
      next: (detail) => {
        this.current.set(detail);
        if (isTerminal(detail.status) && this.watchingId === id) {
          this.stop();
        }
      },
      // A transient poll failure keeps the last known state on screen
      // rather than clearing it — the next tick (or SSE, if connected)
      // will recover it. AuthInterceptor has already attempted a
      // refresh-and-retry before this ever surfaces as an error.
      error: () => {},
    });
  }

  private handleEvent(verificationId: string, event: RealtimeEvent): void {
    if (this.watchingId !== verificationId) return; // watch() moved on to a different id
    const data = event.data as AuditVerificationEventData;
    if (!data || typeof data.status !== 'string') return; // unrecognized/malformed — ignore safely
    this.current.set(data);
    if (isTerminal(data.status)) {
      this.stop();
    }
  }
}
