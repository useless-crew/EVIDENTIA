import { Component, OnDestroy, OnInit, effect, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { ActivatedRoute, Router } from '@angular/router';
import { DmsStateService } from '../../core/services/dms-state.service';
import { CaseService } from '../../core/services/case.service';
import { ApiError } from '../../core/services/api-client.service';
import { EventStreamService } from '../../core/services/event-stream.service';
import { CaseDetail, DocumentSummary } from '../../core/models/api.models';

@Component({
  selector: 'app-case-detail',
  standalone: true,
  imports: [CommonModule],
  templateUrl: './case-detail.component.html',
  styleUrls: ['./case-detail.component.css']
})
export class CaseDetailComponent implements OnInit, OnDestroy {
  dms = inject(DmsStateService);
  private readonly caseService = inject(CaseService);
  private readonly route = inject(ActivatedRoute);
  private readonly router = inject(Router);
  private readonly eventStream = inject(EventStreamService);

  viewMode: 'grid' | 'list' = 'grid';

  readonly caseId = signal<string>('');
  readonly loading = signal(true);
  readonly errorMessage = signal<string | null>(null);
  readonly detail = signal<CaseDetail | null>(null);
  /** System 13: whether this case's real-time event stream
   * (GET /cases/:id/events) is currently connected — a subtle indicator
   * only; the page remains fully usable via REST regardless (see
   * docs/REALTIME_EVENTS.md's "Offline / Disconnected State" — a missed
   * or dropped stream never means lost state, only slightly-less-live
   * updates). */
  readonly liveConnected = signal(false);

  private stopEventStream: (() => void) | null = null;

  constructor() {
    // A document uploaded (via the shared upload modal, opened from
    // this component's own "Upload Document" button below) for THIS case
    // means the documents grid is now stale — refetch. The modal lives in
    // WorkspaceShellComponent, outside this component's tree, so this is
    // the only way this component learns a relevant upload completed.
    effect(() => {
      const uploadedAt = this.dms.documentUploaded();
      if (uploadedAt > 0 && this.dms.uploadCaseId() === this.caseId() && this.caseId()) {
        this.fetch();
      }
    });
  }

  ngOnInit() {
    this.route.paramMap.subscribe((params) => {
      const id = params.get('caseId');
      if (id) {
        this.caseId.set(id);
        this.fetch();
        this.watchEvents(id);
      }
    });
  }

  ngOnDestroy() {
    this.stopEventStream?.();
  }

  /** System 13: subscribes to this case's real-time notification stream
   * — DOCUMENT_VERIFICATION_COMPLETED/CERTIFICATE_GENERATION_COMPLETED/
   * DOCUMENT_REDACTION_COMPLETED/SHARE_CREATED/SHARE_REVOKED (see
   * internal/events/catalog.go). Every one of these is treated identically
   * here: a signal that THIS case's state may have changed, never a
   * replacement for the authoritative refetch below — the event's own
   * `data` is never rendered directly (see docs/REALTIME_EVENTS.md's
   * "TanStack Query"-equivalent guidance: SSE triggers invalidation/
   * refetch, it is not itself application state). Safe to call again for
   * a different case id — stops the previous subscription first. */
  private watchEvents(caseId: string): void {
    this.stopEventStream?.();
    this.stopEventStream = this.eventStream.connect(
      `/cases/${caseId}/events`,
      () => {
        if (this.caseId() === caseId) {
          this.fetch();
        }
      },
      (connected) => this.liveConnected.set(connected),
    );
  }

  fetch() {
    const id = this.caseId();
    if (!id) return;
    this.loading.set(true);
    this.errorMessage.set(null);
    this.caseService.get(id).subscribe({
      next: (d) => {
        this.detail.set(d);
        this.loading.set(false);
      },
      error: (err: ApiError) => {
        this.errorMessage.set(err.message);
        this.loading.set(false);
      },
    });
  }

  openDoc(d: DocumentSummary) {
    this.router.navigate(['/app/cases', this.caseId(), 'documents', d.id]);
  }

  openUpload() {
    this.dms.openUploadModal(this.caseId());
  }

  goToAccess() {
    this.router.navigate(['/app/access-preview']);
  }

  formatStatus(status: string): string {
    return status.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
  }

  getStatusClass(status: string): string {
    switch (status) {
      case 'UNDER_INVESTIGATION': return 'badge-investigation';
      case 'SUBMITTED':
      case 'UNDER_REVIEW': return 'badge-chargesheet';
      case 'CLOSED':
      case 'ARCHIVED': return 'badge-closed';
      default: return 'badge-investigation';
    }
  }

  getDocBadgeClass(status: string): string {
    return status === 'TAMPERED' ? 'badge-danger' : 'badge-chargesheet';
  }

  getDotColor(status: string): string {
    return status === 'TAMPERED' ? '#c53030' : '#2e7d4f';
  }

  canUpload(): boolean {
    const r = this.dms.role();
    return r === 'Police' || r === 'Forensics' || r === 'Admin';
  }
}
