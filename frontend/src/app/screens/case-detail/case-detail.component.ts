import { Component, OnDestroy, OnInit, effect, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { ActivatedRoute, Router } from '@angular/router';
import { DmsStateService } from '../../core/services/dms-state.service';
import { CaseService } from '../../core/services/case.service';
import { ApiError } from '../../core/services/api-client.service';
import { EventStreamService } from '../../core/services/event-stream.service';
import { CaseDetail, CaseMemberSummary, CaseStatus, DocumentSummary } from '../../core/models/api.models';
import { RevealDirective } from '../../core/directives/reveal.directive';
import { AddMemberDialogComponent } from '../../components/add-member-dialog/add-member-dialog.component';

@Component({
  selector: 'app-case-detail',
  standalone: true,
  imports: [CommonModule, RevealDirective, AddMemberDialogComponent],
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
  readonly members = signal<CaseMemberSummary[]>([]);
  readonly loadingMembers = signal(false);
  readonly showAddMemberModal = signal(false);
  readonly removingMemberId = signal<string | null>(null);
  /** Allowed next statuses map adhering to backend caseStatusTransitions */
  readonly allowedTransitions: Record<string, CaseStatus[]> = {
    OPEN: ['UNDER_INVESTIGATION', 'ARCHIVED'],
    UNDER_INVESTIGATION: ['SUBMITTED', 'ARCHIVED'],
    SUBMITTED: ['UNDER_REVIEW', 'UNDER_INVESTIGATION', 'ARCHIVED'],
    UNDER_REVIEW: ['CLOSED', 'UNDER_INVESTIGATION', 'ARCHIVED'],
    CLOSED: ['ARCHIVED'],
    ARCHIVED: []
  };

  readonly showStatusMenu = signal(false);
  readonly updatingStatus = signal(false);
  readonly statusError = signal<string | null>(null);

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
    this.loadMembers();
  }

  loadMembers() {
    const id = this.caseId();
    if (!id) return;
    this.loadingMembers.set(true);
    this.caseService.listMembers(id).subscribe({
      next: (m) => {
        this.members.set(m);
        this.loadingMembers.set(false);
      },
      error: () => {
        this.loadingMembers.set(false);
      },
    });
  }

  canManageMembers(): boolean {
    const d = this.detail();
    if (!d) return false;
    return d.relationship?.is_owner || this.dms.role() === 'Admin';
  }

  getAvailableTransitions(): CaseStatus[] {
    const current = this.detail()?.status;
    if (!current) return [];
    return this.allowedTransitions[current] || [];
  }

  toggleStatusMenu() {
    this.showStatusMenu.update(v => !v);
  }

  closeStatusMenu() {
    this.showStatusMenu.set(false);
  }

  changeStatus(nextStatus: CaseStatus) {
    const d = this.detail();
    if (!d || this.updatingStatus()) return;

    this.updatingStatus.set(true);
    this.statusError.set(null);

    this.caseService.update(this.caseId(), {
      title: d.title,
      description: d.description,
      status: nextStatus,
      metadata: d.metadata || {}
    }).subscribe({
      next: (updated) => {
        this.updatingStatus.set(false);
        this.showStatusMenu.set(false);
        this.detail.set(updated);
        this.fetch();
      },
      error: (err: ApiError) => {
        this.updatingStatus.set(false);
        this.statusError.set(err.message || 'Failed to update case status');
      }
    });
  }

  openAssignMember() {
    this.showAddMemberModal.set(true);
  }

  closeAssignMember() {
    this.showAddMemberModal.set(false);
  }

  onMemberAdded(member: CaseMemberSummary) {
    this.loadMembers();
    this.fetch();
  }

  removeMember(member: CaseMemberSummary) {
    if (member.membership_type === 'OWNER') return;
    const confirmMsg = `Remove ${member.display_name} (${member.membership_type}) from this case?`;
    if (!confirm(confirmMsg)) return;

    this.removingMemberId.set(member.user_id);
    this.caseService.removeMember(this.caseId(), member.user_id).subscribe({
      next: () => {
        this.removingMemberId.set(null);
        this.loadMembers();
        this.fetch();
      },
      error: (err: ApiError) => {
        this.removingMemberId.set(null);
        alert(err.message || 'Failed to remove member');
      },
    });
  }

  getMembershipBadgeClass(type: string): string {
    switch (type) {
      case 'OWNER': return 'badge-owner';
      case 'FORENSICS': return 'badge-forensics';
      case 'INVESTIGATOR': return 'badge-investigator';
      case 'LAWYER': return 'badge-lawyer';
      case 'JUDGE': return 'badge-judge';
      default: return 'badge-viewer';
    }
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
