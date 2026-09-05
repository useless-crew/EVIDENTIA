import { Component, EventEmitter, Input, Output, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { ShareService } from '../../core/services/share.service';
import { ApiError } from '../../core/services/api-client.service';
import { RecipientCandidate, ShareSummary, SharePermission } from '../../core/models/api.models';

/**
 * "Share" flow (master prompt §37): search an Evidentia user (never a
 * free-typed ID — GET /users/search's safe, capped, active-only
 * candidates only), pick VIEW or VERIFY, optionally set an expiration,
 * confirm. The backend is authoritative for every decision here — this
 * component never computes a hash, never assumes a permission is valid,
 * and never grants access itself; a successful POST is the only thing
 * that creates a share.
 */
@Component({
  selector: 'app-share-dialog',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './share-dialog.component.html',
  styleUrls: ['./share-dialog.component.css'],
})
export class ShareDialogComponent {
  private readonly shareService = inject(ShareService);

  @Input({ required: true }) documentId!: string;
  @Input() documentFilename = '';
  @Output() closed = new EventEmitter<void>();
  @Output() shared = new EventEmitter<ShareSummary>();

  query = '';
  readonly candidates = signal<RecipientCandidate[]>([]);
  readonly searching = signal(false);
  readonly selectedRecipient = signal<RecipientCandidate | null>(null);

  permission: SharePermission = 'VIEW';
  hasExpiration = false;
  expiresAt = '';
  reason = '';

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
  }

  clearRecipient() {
    this.selectedRecipient.set(null);
    this.query = '';
  }

  submit() {
    if (this.submitting()) return;
    const recipient = this.selectedRecipient();
    if (!recipient) {
      this.error.set('Search for and select an Evidentia user to share with.');
      return;
    }
    if (this.hasExpiration && !this.expiresAt) {
      this.error.set('Choose an expiration date/time, or turn off expiration.');
      return;
    }

    this.submitting.set(true);
    this.error.set(null);
    this.shareService
      .create(this.documentId, {
        user_id: recipient.id,
        permission: this.permission,
        expires_at: this.hasExpiration ? new Date(this.expiresAt).toISOString() : undefined,
        reason: this.reason.trim() || undefined,
      })
      .subscribe({
        next: (summary) => {
          this.submitting.set(false);
          this.shared.emit(summary);
        },
        error: (err: ApiError) => {
          this.submitting.set(false);
          this.error.set(this.friendlyError(err));
        },
      });
  }

  private friendlyError(err: ApiError): string {
    switch (err.status) {
      case 400:
        return err.message || 'Invalid recipient, permission, or expiration.';
      case 403:
        return 'You do not have permission to share this document.';
      case 409:
        return 'This document is already actively shared with that user.';
      default:
        return err.message || 'Sharing failed. Please try again.';
    }
  }

  cancel() {
    this.closed.emit();
  }
}
