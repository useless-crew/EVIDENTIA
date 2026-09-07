import { Component, EventEmitter, Input, Output, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { CaseService } from '../../core/services/case.service';
import { ShareService } from '../../core/services/share.service';
import { ApiError } from '../../core/services/api-client.service';
import { CaseMemberSummary, RecipientCandidate } from '../../core/models/api.models';

@Component({
  selector: 'app-add-member-dialog',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './add-member-dialog.component.html',
  styleUrls: ['./add-member-dialog.component.css'],
})
export class AddMemberDialogComponent {
  private readonly caseService = inject(CaseService);
  private readonly shareService = inject(ShareService);

  @Input({ required: true }) caseId!: string;
  @Input() caseNumber = '';
  @Output() closed = new EventEmitter<void>();
  @Output() memberAdded = new EventEmitter<CaseMemberSummary>();

  query = '';
  readonly candidates = signal<RecipientCandidate[]>([]);
  readonly searching = signal(false);
  readonly selectedRecipient = signal<RecipientCandidate | null>(null);

  membershipType = 'FORENSICS';
  readonly submitting = signal(false);
  readonly error = signal<string | null>(null);

  private searchTimer: ReturnType<typeof setTimeout> | null = null;

  onQueryInput(value: string) {
    this.query = value;
    this.selectedRecipient.set(null);
    if (this.searchTimer) clearTimeout(this.searchTimer);

    if (value.trim().length < 2) {
      this.candidates.set([]);
      return;
    }
    this.searchTimer = setTimeout(() => this.runSearch(value.trim()), 250);
  }

  private runSearch(q: string) {
    this.searching.set(true);
    this.shareService.searchRecipients(q).subscribe({
      next: (results) => {
        this.searching.set(false);
        this.candidates.set(results);
      },
      error: () => {
        this.searching.set(false);
        this.candidates.set([]);
      },
    });
  }

  selectRecipient(candidate: RecipientCandidate) {
    this.selectedRecipient.set(candidate);
    this.candidates.set([]);
    this.query = `${candidate.first_name} ${candidate.last_name} (${candidate.email})`;

    // Auto-detect best matching membership role based on user's system roles
    const roles = (candidate.roles || []).map((r) => r.toUpperCase());
    if (roles.includes('FORENSICS')) {
      this.membershipType = 'FORENSICS';
    } else if (roles.includes('LAWYER')) {
      this.membershipType = 'LAWYER';
    } else if (roles.includes('JUDGE')) {
      this.membershipType = 'JUDGE';
    } else if (roles.includes('POLICE') || roles.includes('INVESTIGATOR')) {
      this.membershipType = 'INVESTIGATOR';
    } else {
      this.membershipType = 'FORENSICS';
    }
  }

  clearRecipient() {
    this.selectedRecipient.set(null);
    this.query = '';
  }

  cancel() {
    this.closed.emit();
  }

  submit() {
    if (this.submitting()) return;
    const recipient = this.selectedRecipient();
    if (!recipient) {
      this.error.set('Search for and select an Evidentia officer or personnel.');
      return;
    }

    this.submitting.set(true);
    this.error.set(null);

    this.caseService
      .addMember(this.caseId, {
        user_id: recipient.id,
        membership_type: this.membershipType,
      })
      .subscribe({
        next: (created) => {
          this.submitting.set(false);
          this.memberAdded.emit(created);
          this.closed.emit();
        },
        error: (err: ApiError) => {
          this.submitting.set(false);
          this.error.set(err.message || 'Failed to assign member to case');
        },
      });
  }
}
