import { Component, OnDestroy, OnInit, computed, effect, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { AuditVerificationService } from '../../core/services/audit-verification.service';
import { AuditService } from '../../core/services/audit.service';
import { AuthService } from '../../core/services/auth.service';
import { DmsStateService } from '../../core/services/dms-state.service';
import { ApiError } from '../../core/services/api-client.service';
import {
  AuditEntry,
  AuditListResult,
  AuditVerificationEventData,
  IntegritySummary,
  VerificationDetail,
  VerificationHistoryResult,
} from '../../core/models/api.models';

const AUDIT_PAGE_SIZE = 20;

/** Maps a real backend action string (see internal/audit's Action
 * constants) to one of the Ledger Table's badge categories. Deliberately
 * a small, explicit allowlist rather than a pattern match — an action
 * this codebase adds later that isn't listed here safely falls through
 * to 'default' rather than being mis-categorized. */
const ACTION_TYPE_BY_ACTION: Record<string, string> = {
  DOCUMENT_UPLOADED: 'upload',
  DOCUMENT_ADDED: 'upload',
  DOCUMENT_VERIFIED: 'verify',
  DOCUMENT_INTEGRITY_FAILURE: 'denied',
  DOCUMENT_DOWNLOADED: 'view',
  DOCUMENT_REDACTED: 'redact',
  DOCUMENT_SHARED: 'status',
  DOCUMENT_SHARE_REVOKED: 'status',
  CERTIFICATE_CREATED: 'hash',
  CASE_CREATED: 'status',
  CASE_UPDATED: 'status',
  CASE_STATUS_CHANGED: 'status',
  INVOLVED_PARTY_ADDED: 'status',
  USER_CREATED: 'status',
  USER_UPDATED: 'status',
  USER_ROLE_CHANGED: 'status',
  USER_STATUS_CHANGED: 'status',
  USER_PASSWORD_RESET: 'status',
  AUDIT_ACCESSED: 'view',
  AUDIT_CHAIN_VERIFICATION_REQUESTED: 'verify',
  AUDIT_CHAIN_VERIFICATION_COMPLETED: 'verify',
  AUTHZ_DENIED: 'denied',
  AUTH_LOGIN_FAILED: 'denied',
  AUTH_LOGIN_RATE_LIMITED: 'denied',
  AUTH_REFRESH_FAILED: 'denied',
  AUTH_REFRESH_REUSE_DETECTED: 'denied',
};

@Component({
  selector: 'app-audit-log',
  standalone: true,
  imports: [CommonModule],
  templateUrl: './audit-log.component.html',
  styleUrls: ['./audit-log.component.css'],
})
export class AuditLogComponent implements OnInit, OnDestroy {
  dms = inject(DmsStateService);
  private readonly auth = inject(AuthService);
  private readonly verification = inject(AuditVerificationService);
  private readonly auditService = inject(AuditService);

  /** UX-only gate — the backend independently re-checks audit:verify on
   * every one of these routes (RBAC + audit_verifications' own RLS), so a
   * manipulated frontend gains nothing; this only avoids rendering
   * controls a non-admin's very first API call would reject anyway. */
  readonly isAdmin = computed(() => this.auth.role() === 'ADMIN');

  readonly live = this.verification.current;
  readonly sseConnected = this.verification.sseConnected;

  readonly summary = signal<IntegritySummary | null>(null);
  readonly summaryLoading = signal(false);
  readonly summaryError = signal<string | null>(null);

  readonly history = signal<VerificationHistoryResult | null>(null);
  readonly historyLoading = signal(false);
  readonly historyError = signal<string | null>(null);

  readonly startError = signal<string | null>(null);

  // ---- Ledger Table tab (System 16 — real GET /audit data) ----
  readonly auditResult = signal<AuditListResult | null>(null);
  readonly auditPage = signal(1);
  readonly auditLoading = signal(false);
  readonly auditError = signal<string | null>(null);

  /** True while a verification this component knows about is QUEUED or
   * RUNNING — drives the Verify button's disabled state and the progress
   * banner. Prefers the live-watched value; falls back to the summary's
   * last-known run so a page reload while a verification is mid-flight
   * still shows it as in-progress rather than silently forgetting it. */
  readonly activeStatus = computed<string | undefined>(() => {
    const live = this.live();
    if (live) return live.status;
    return this.summary()?.last_verification?.status;
  });

  readonly isRunning = computed(() => {
    const status = this.activeStatus();
    return status === 'QUEUED' || status === 'RUNNING';
  });

  private lastSettledId: string | null = null;

  constructor() {
    // Reloads the summary/history cards the moment a WATCHED verification
    // reaches a terminal state — a plain signal `effect`, not a manual
    // callback threaded through the service, so this stays correct
    // regardless of whether the terminal state arrived via SSE or the
    // poll-timer backstop (see AuditVerificationService.watch).
    effect(() => {
      const detail = this.live();
      if (!detail || !this.isTerminal(detail.status)) return;
      if (this.lastSettledId === detail.verification_id) return;
      this.lastSettledId = detail.verification_id;
      this.onVerificationSettled();
    });
  }

  ngOnInit(): void {
    // The Ledger Table (audit:read) and the Integrity dashboard
    // (audit:verify, ADMIN-only per the seed data) are independent
    // authorization scopes — see AuditService's/AuditVerificationService's
    // own doc comments — so each loads unconditionally on its own gate,
    // never on isAdmin() for both. FORENSICS (no audit:read at all) gets
    // a clean 403 handled by fetchAuditEntries's own error signal, same
    // as any other unauthorized role hitting any other RBAC-gated route.
    this.fetchAuditEntries(1);
    if (this.isAdmin()) {
      this.loadDashboard();
    }
  }

  fetchAuditEntries(page: number): void {
    this.auditPage.set(page);
    this.auditLoading.set(true);
    this.auditError.set(null);
    this.auditService.list({ page, page_size: AUDIT_PAGE_SIZE }).subscribe({
      next: (result) => {
        this.auditResult.set(result);
        this.auditLoading.set(false);
      },
      error: (err: ApiError) => {
        this.auditLoading.set(false);
        this.auditError.set(err.message);
      },
    });
  }

  goToAuditPage(p: number): void {
    const meta = this.auditResult()?.meta;
    if (!meta || p < 1 || p > meta.total_pages) return;
    this.fetchAuditEntries(p);
  }

  /** A short, stable label for an entry's actor — this codebase does not
   * resolve user_id to a display name on the audit entry itself (see
   * internal/service.AuditEntrySummary), so a truncated UUID is the
   * honest, non-fabricated representation; a background/system action
   * (see internal/service's workerIdentity) still carries a real actor
   * UUID, never a null placeholder invented client-side. */
  actorLabel(entry: AuditEntry): string {
    return entry.user_id ? entry.user_id.slice(0, 8) : '—';
  }

  /** e.g. "document · 3f9a21b2" — resource_type plus a truncated
   * resource_id, since the audit entry itself carries no denormalized
   * resource name (see AuditEntrySummary's doc comment). */
  resourceLabel(entry: AuditEntry): string {
    return entry.resource_id
      ? `${entry.resource_type} · ${entry.resource_id.slice(0, 8)}`
      : entry.resource_type;
  }

  /** "DOCUMENT_UPLOADED" -> "Document Uploaded" — display formatting
   * only; the underlying value passed to filters/exports stays the raw
   * backend action string. */
  formatAction(action: string): string {
    return action
      .toLowerCase()
      .replace(/_/g, ' ')
      .replace(/\b\w/g, (c) => c.toUpperCase());
  }

  auditActionBadgeClass(action: string): string {
    return this.getActionBadgeClass(ACTION_TYPE_BY_ACTION[action] ?? 'default');
  }

  ngOnDestroy(): void {
    this.verification.stop();
  }

  loadDashboard(): void {
    this.loadSummary();
    this.loadHistory();
  }

  loadSummary(): void {
    this.summaryLoading.set(true);
    this.summaryError.set(null);
    this.verification.getIntegritySummary().subscribe({
      next: (result) => {
        this.summary.set(result);
        this.summaryLoading.set(false);
        const last = result.last_verification;
        if (last && (last.status === 'QUEUED' || last.status === 'RUNNING')) {
          this.verification.watch(last.verification_id);
        }
      },
      error: (err: ApiError) => {
        this.summaryLoading.set(false);
        this.summaryError.set(err.message);
      },
    });
  }

  loadHistory(): void {
    this.historyLoading.set(true);
    this.historyError.set(null);
    this.verification.getVerificationHistory({ page: 1, page_size: 10 }).subscribe({
      next: (result) => {
        this.history.set(result);
        this.historyLoading.set(false);
      },
      error: (err: ApiError) => {
        this.historyLoading.set(false);
        this.historyError.set(err.message);
      },
    });
  }

  onVerifyClick(): void {
    if (this.isRunning()) return;
    this.startError.set(null);
    this.verification.startVerification().subscribe({
      next: (result) => {
        this.verification.watch(result.verification_id);
      },
      error: (err: ApiError) => {
        this.startError.set(err.message);
      },
    });
  }

  /** Refreshes the summary/history once a watched verification reaches a
   * terminal state, so the dashboard's "last verification" card and the
   * history list reflect it without a manual page reload. */
  onVerificationSettled(): void {
    this.loadDashboard();
  }

  isTerminal(status: string | undefined): boolean {
    return status === 'VERIFIED' || status === 'INTEGRITY_FAILURE' || status === 'FAILED';
  }

  statusLabel(status: string | undefined): string {
    switch (status) {
      case 'VERIFIED':
        return 'AUDIT CHAIN VERIFIED';
      case 'INTEGRITY_FAILURE':
        return 'INTEGRITY FAILURE DETECTED';
      case 'FAILED':
        return 'VERIFICATION FAILED';
      case 'RUNNING':
        return 'VERIFYING…';
      case 'QUEUED':
        return 'QUEUED';
      default:
        return 'NO VERIFICATION YET';
    }
  }

  statusClass(status: string | undefined): string {
    switch (status) {
      case 'VERIFIED':
        return 'status-verified';
      case 'INTEGRITY_FAILURE':
        return 'status-integrity-failure';
      case 'FAILED':
        return 'status-failed';
      case 'RUNNING':
      case 'QUEUED':
        return 'status-running';
      default:
        return 'status-unknown';
    }
  }

  progressLabel(detail: VerificationDetail | AuditVerificationEventData | null): string {
    if (!detail) return '';
    const total = detail.total_entries;
    if (total === undefined) {
      return `Verifying audit chain… ${detail.entries_checked.toLocaleString()} entries checked`;
    }
    return `Verifying ${detail.entries_checked.toLocaleString()} of ${total.toLocaleString()} audit entries`;
  }

  setTab(tab: 'table' | 'graph') {
    this.dms.auditTab.set(tab);
  }

  toggleRow(id: string) {
    this.dms.toggleAuditRow(id);
  }

  getActionBadgeClass(actionType: string): string {
    switch (actionType) {
      case 'upload':
        return 'action-upload';
      case 'hash':
        return 'action-hash';
      case 'status':
        return 'action-status';
      case 'view':
        return 'action-view';
      case 'redact':
        return 'action-redact';
      case 'denied':
        return 'action-denied';
      case 'verify':
        return 'action-verify';
      default:
        return 'action-default';
    }
  }
}
