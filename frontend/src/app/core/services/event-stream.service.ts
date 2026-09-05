import { Injectable, inject } from '@angular/core';
import { environment } from '../../../environments/environment';
import { RealtimeEvent } from '../models/api.models';
import { AuthService } from './auth.service';

/**
 * System 13's ONE central Server-Sent Events client — every SSE
 * connection this frontend ever opens (audit-chain verification's
 * progress stream, a case's real-time notification stream) goes through
 * this service, never a separate raw `EventSource`/`fetch()` parsing loop
 * per feature (see docs/REALTIME_EVENTS.md's "Frontend SSE Client").
 *
 * Uses `fetch()`, not `EventSource`, so a normal `Authorization: Bearer
 * <token>` header can be attached — the browser's native `EventSource`
 * API cannot set request headers at all, and this project's backend
 * requires the same bearer-token authentication on this route as any
 * other (never a token embedded in the URL, which would leak into
 * server/proxy access logs and browser history — see this method's own
 * doc comment). Every connection this service opens is bounded by an
 * `AbortController` the caller gets back as a `stop()` function; callers
 * are expected to call it on component `ngOnDestroy` or when switching to
 * a different subscription.
 *
 * This service performs NO reconnect loop of its own: a dropped
 * connection simply reports `onConnectionChange(false)` and stops. The
 * backend is authoritative for every fact an event describes (see
 * internal/events' own doc comment) — a caller that wants live updates to
 * resume after a drop is expected to layer its own REST poll backstop
 * and/or explicit reconnect on top (see AuditVerificationService.watch
 * for the established pattern: a REST poll timer that keeps running
 * regardless of whether SSE is currently connected, so a dropped stream
 * degrades to "slightly less live", never to "stuck on stale data").
 */
@Injectable({ providedIn: 'root' })
export class EventStreamService {
  private readonly auth = inject(AuthService);

  /**
   * Opens an authenticated SSE connection to `path` (relative to
   * environment.apiBaseUrl, e.g. `/cases/{id}/events`). onEvent is called
   * for each successfully decoded RealtimeEvent — a malformed frame or an
   * event_type the caller doesn't recognize is the CALLER's concern to
   * ignore safely (this service never crashes or throws over one bad
   * event; see parseFrame's own doc comment). onConnectionChange, if
   * given, reports true once the connection is actually open and false
   * on any disconnect (including a clean server-side stream end).
   *
   * Returns a stop function the caller MUST call exactly once (component
   * ngOnDestroy, or before opening a different subscription) — it aborts
   * the underlying fetch and is always safe to call even if the
   * connection never successfully opened.
   */
  connect(
    path: string,
    onEvent: (event: RealtimeEvent) => void,
    onConnectionChange?: (connected: boolean) => void,
  ): () => void {
    const token = this.auth.accessToken();
    if (!token) {
      // No session — nothing to connect with; the caller's own REST calls
      // will already be failing/redirecting via the same auth state.
      return () => {};
    }

    const controller = new AbortController();
    const url = `${environment.apiBaseUrl}${path}`;

    fetch(url, {
      headers: { Authorization: `Bearer ${token}` },
      signal: controller.signal,
    })
      .then((response) => this.readStream(response, onEvent, onConnectionChange))
      .catch((err: unknown) => {
        if (this.isAbortError(err)) return; // intentional disconnect — not a failure
        onConnectionChange?.(false);
      });

    return () => controller.abort();
  }

  private async readStream(
    response: Response,
    onEvent: (event: RealtimeEvent) => void,
    onConnectionChange?: (connected: boolean) => void,
  ): Promise<void> {
    if (!response.ok || !response.body) {
      onConnectionChange?.(false);
      return;
    }
    onConnectionChange?.(true);

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
          this.parseFrame(frame, onEvent);
        }
      }
    } catch (err) {
      if (!this.isAbortError(err)) {
        // Stream broke mid-read — the caller's own onConnectionChange(false)
        // below is the only signal; no reconnect loop here (see this
        // service's own doc comment).
      }
    } finally {
      onConnectionChange?.(false);
    }
  }

  /** Parses one `\n\n`-delimited SSE frame. A heartbeat (`: heartbeat`)
   * carries no `data:` line and is correctly ignored here — it exists
   * only to keep the connection alive through intermediate proxies. A
   * `data:` line that fails to parse as JSON is also ignored (never
   * thrown) — a malformed event must never crash the caller. */
  private parseFrame(frame: string, onEvent: (event: RealtimeEvent) => void): void {
    let data = '';
    for (const line of frame.split('\n')) {
      if (line.startsWith('data:')) {
        data += line.slice(5).trimStart();
      }
    }
    if (!data) return;

    try {
      onEvent(JSON.parse(data) as RealtimeEvent);
    } catch {
      // Malformed frame — ignore it.
    }
  }

  private isAbortError(err: unknown): boolean {
    return err instanceof DOMException && err.name === 'AbortError';
  }
}
