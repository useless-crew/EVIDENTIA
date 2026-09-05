import { Injectable, inject, signal } from '@angular/core';
import { Observable } from 'rxjs';
import { environment } from '../../../environments/environment';
import {
  IntegritySummary,
  StartVerificationResponse,
  VerificationDetail,
  VerificationHistoryFilter,
  VerificationHistoryResult,
  VerificationSseEvent,
} from '../models/api.models';
import { ApiClientService } from './api-client.service';
import { AuthService } from './auth.service';

/** Terminal — VerificationStatus values that will never change again. */
const TERMINAL_STATUSES = new Set(['VERIFIED', 'INTEGRITY_FAILURE', 'FAILED']);

function isTerminal(status: string | undefined): boolean {
  return !!status && TERMINAL_STATUSES.has(status);
}

/**
 * System 11's audit-chain verification client: REST calls (via the shared
 * ApiClientService — no scattered fetch()/HttpClient use elsewhere) plus a
 * live-progress watcher combining Server-Sent Events with a REST polling
 * backstop.
 *
 * Why a backstop in addition to SSE: the REST status endpoint, not
 * accumulated SSE events, is always treated as the source of truth for
 * "what is the CURRENT state" (see docs/AUDIT_CHAIN.md's "SSE
 * reconnection") — every poll goes through ApiClientService/HttpClient,
 * which means AuthInterceptor's existing 401-triggers-one-refresh-and-
 * retry logic covers it automatically. The SSE half (started via
 * `fetch()`, not `EventSource`, so a normal Authorization header can be
 * attached — see docs/AUDIT_CHAIN.md for why `EventSource` can't do this)
 * exists purely so the dashboard updates quickly; if it drops, disconnects,
 * or errors, watch() simply keeps relying on the poll timer already
 * running underneath it — never leaving the UI stuck on stale data purely
 * because a stream connection failed.
 *
 * The backend is authoritative for VERIFIED/INTEGRITY_FAILURE/progress —
 * this service never computes any of those itself; it only relays
 * whatever the server returns.
 */
@Injectable({ providedIn: 'root' })
export class AuditVerificationService {
  private readonly api = inject(ApiClientService);
  private readonly auth = inject(AuthService);

  /** The verification currently being watched, or null if none. Updated
   * by both the poll timer and SSE frames — always server-derived. */
  readonly current = signal<VerificationDetail | VerificationSseEvent | null>(null);
  readonly sseConnected = signal(false);
  readonly watchError = signal<string | null>(null);

  private watchingId: string | null = null;
  private pollHandle: ReturnType<typeof setInterval> | null = null;
  private abortController: AbortController | null = null;

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
    this.connectSse(verificationId);
  }

  /** Stop watching — closes the SSE connection (if any) and the poll
   * timer. Always safe to call, including when nothing is being watched
   * (component ngOnDestroy/navigation-away). */
  stop(): void {
    this.abortController?.abort();
    this.abortController = null;
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

  private connectSse(verificationId: string): void {
    const token = this.auth.accessToken();
    if (!token) return; // no session — refreshStatus()'s own 401 handling covers this

    this.abortController = new AbortController();
    const url = `${environment.apiBaseUrl}/audit/verify-chain/${verificationId}/events`;

    fetch(url, {
      headers: { Authorization: `Bearer ${token}` },
      signal: this.abortController.signal,
    })
      .then((response) => this.readSseStream(verificationId, response))
      .catch((err: unknown) => {
        if (this.isAbortError(err)) return; // intentional disconnect — not a failure
        this.sseConnected.set(false);
        // No reconnect loop here: the poll timer started by watch() is
        // already running and is the authoritative fallback.
      });
  }

  private async readSseStream(verificationId: string, response: Response): Promise<void> {
    if (!response.ok || !response.body) {
      this.sseConnected.set(false);
      return;
    }
    this.sseConnected.set(true);

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';

    try {
      // eslint-disable-next-line no-constant-condition
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });

        let separatorIndex: number;
        while ((separatorIndex = buffer.indexOf('\n\n')) >= 0) {
          const frame = buffer.slice(0, separatorIndex);
          buffer = buffer.slice(separatorIndex + 2);
          this.handleSseFrame(verificationId, frame);
        }
      }
    } catch (err) {
      if (!this.isAbortError(err)) {
        // Stream broke mid-read — same story as a failed initial
        // connection: rely on the poll timer, don't loop reconnecting.
      }
    } finally {
      this.sseConnected.set(false);
    }
  }

  private handleSseFrame(verificationId: string, frame: string): void {
    if (this.watchingId !== verificationId) return; // watch() moved on to a different id

    let data = '';
    for (const line of frame.split('\n')) {
      if (line.startsWith('data:')) {
        data += line.slice(5).trimStart();
      }
      // Heartbeat lines (`: heartbeat`) carry no `data:` line and are
      // correctly ignored here — they exist only to keep the connection
      // alive through intermediate proxies.
    }
    if (!data) return;

    try {
      const event = JSON.parse(data) as VerificationSseEvent;
      this.current.set(event);
      if (isTerminal(event.status)) {
        this.stop();
      }
    } catch {
      // Malformed frame — ignore it; the poll timer keeps state current.
    }
  }

  private isAbortError(err: unknown): boolean {
    return err instanceof DOMException && err.name === 'AbortError';
  }
}
