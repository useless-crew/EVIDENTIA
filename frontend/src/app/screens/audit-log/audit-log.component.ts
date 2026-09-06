import { Component, OnDestroy, OnInit, effect, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { AuditService } from '../../core/services/audit.service';
import { AuditVerificationService } from '../../core/services/audit-verification.service';
import { ApiError } from '../../core/services/api-client.service';
import { AuditEntry, IntegritySummary, PageMeta } from '../../core/models/api.models';

const PAGE_SIZE = 20;

/**
 * System 10/11 — real integration. The ledger table is GET /audit
 * (AuditService), paginated and server-authoritative; "Verify Full Chain"
 * starts the real Asynq-backed job (POST /audit/verify-chain) and follows
 * its progress via AuditVerificationService.watch() (SSE with a REST-poll
 * backstop — the REST status endpoint is always the source of truth). No
 * audit entry, hash, or verification outcome shown here is computed or
 * fabricated client-side.
 */
@Component({
  selector: 'app-audit-log',
  standalone: true,
  imports: [CommonModule],
  templateUrl: './audit-log.component.html',
  styleUrls: ['./audit-log.component.css']
})
export class AuditLogComponent implements OnInit, OnDestroy {
  private readonly auditService = inject(AuditService);
  readonly verification = inject(AuditVerificationService);

  readonly loading = signal(true);
  readonly errorMessage = signal<string | null>(null);
  readonly entries = signal<AuditEntry[]>([]);
  readonly meta = signal<PageMeta | null>(null);
  readonly page = signal(1);
  readonly expandedId = signal<string | null>(null);

  // GET /audit/integrity requires audit:verify (ADMIN-only, per the seed
  // role/permission matrix) — its success/failure is how this screen
  // decides whether to show chain-verification controls at all, rather
  // than hardcoding a role check the backend could someday diverge from.
  readonly canVerify = signal(false);
  readonly integrity = signal<IntegritySummary | null>(null);
  readonly integrityError = signal<string | null>(null);

  readonly starting = signal(false);
  readonly startError = signal<string | null>(null);

  private lastNotifiedStatus: string | null = null;

  constructor() {
    // Refresh the ledger + integrity summary exactly once when a watched
    // verification reaches a terminal state — VerificationDetail/
    // AuditVerificationEventData both use this same field name (see
    // AuditVerificationService.current's doc comment).
    effect(() => {
      const status = this.verification.current()?.status;
      const terminal = status === 'VERIFIED' || status === 'INTEGRITY_FAILURE' || status === 'FAILED';
      if (terminal && status !== this.lastNotifiedStatus) {
        this.lastNotifiedStatus = status;
        this.onVerificationSettled();
      } else if (!terminal) {
        this.lastNotifiedStatus = null;
      }
    });
  }

  ngOnInit() {
    this.loadEntries();
    this.loadIntegrity();
  }

  ngOnDestroy() {
    this.verification.stop();
  }

  loadEntries() {
    this.loading.set(true);
    this.errorMessage.set(null);
    this.auditService.list({ page: this.page(), page_size: PAGE_SIZE }).subscribe({
      next: (result) => {
        this.entries.set(result.entries);
        this.meta.set(result.meta);
        this.loading.set(false);
      },
      error: (err: ApiError) => {
        this.errorMessage.set(err.message);
        this.loading.set(false);
      },
    });
  }

  private loadIntegrity() {
    this.verification.getIntegritySummary().subscribe({
      next: (summary) => {
        this.integrity.set(summary);
        this.canVerify.set(true);
      },
      error: (err: ApiError) => {
        this.canVerify.set(false);
        // A 403 here just means this role has no audit:verify permission
        // — expected for most roles, not an error to surface.
        if (err.status !== 403) this.integrityError.set(err.message);
      },
    });
  }

  nextPage() {
    const m = this.meta();
    if (!m || this.page() >= m.total_pages) return;
    this.page.set(this.page() + 1);
    this.loadEntries();
  }

  prevPage() {
    if (this.page() <= 1) return;
    this.page.set(this.page() - 1);
    this.loadEntries();
  }

  toggleRow(id: string) {
    this.expandedId.set(this.expandedId() === id ? null : id);
  }

  startVerify() {
    if (this.starting() || this.isVerificationActive()) return;
    this.starting.set(true);
    this.startError.set(null);
    this.verification.startVerification().subscribe({
      next: (res) => {
        this.starting.set(false);
        this.verification.watch(res.verification_id);
      },
      error: (err: ApiError) => {
        this.starting.set(false);
        this.startError.set(err.message);
      },
    });
  }

  /** Refresh the ledger + integrity summary after a verification completes
   * — a completed run may have appended new CERTIFICATE/AUDIT_* entries. */
  onVerificationSettled() {
    this.loadIntegrity();
    this.loadEntries();
  }

  isVerificationActive(): boolean {
    const status = this.verification.current()?.status;
    return status === 'QUEUED' || status === 'RUNNING';
  }

  progressPercent(): number {
    const current = this.verification.current();
    if (!current) return 0;
    if (typeof current.progress_percent === 'number') return current.progress_percent;
    const total = current.total_entries;
    if (total && total > 0) return Math.round((current.entries_checked / total) * 100);
    return 0;
  }

  getActionBadgeClass(action: string): string {
    const a = (action || '').toUpperCase();
    if (a.includes('UPLOAD')) return 'action-upload';
    if (a.includes('VERIF')) return 'action-verify';
    if (a.includes('REDACT')) return 'action-redact';
    if (a.includes('DENIED') || a.includes('FAILURE') || a.includes('FAILED')) return 'action-denied';
    if (a.includes('VIEW') || a.includes('DOWNLOAD') || a.includes('ACCESS')) return 'action-view';
    if (a.includes('STATUS') || a.includes('ROLE') || a.includes('CREATED') || a.includes('UPDATED')) return 'action-status';
    return 'action-default';
  }
}
