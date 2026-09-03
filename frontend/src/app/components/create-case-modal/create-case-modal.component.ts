import { Component, EventEmitter, Output, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { CaseService } from '../../core/services/case.service';
import { ApiError } from '../../core/services/api-client.service';
import { CaseDetail } from '../../core/models/api.models';

/**
 * POST /cases, in a small modal — there was previously no case-creation
 * UI at all (CasesComponent's "New Case" button just re-opened the
 * hardcoded demo case detail view). case_number/title are required by
 * the backend (see docs/API_ENDPOINTS.md's "Cases" section);
 * status/created_by/created_at are never sent — the backend controls all
 * three (status defaults to OPEN, created_by is always the authenticated
 * caller).
 */
@Component({
  selector: 'app-create-case-modal',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './create-case-modal.component.html',
  styleUrls: ['./create-case-modal.component.css'],
})
export class CreateCaseModalComponent {
  @Output() closed = new EventEmitter<void>();
  @Output() created = new EventEmitter<CaseDetail>();

  private readonly caseService = inject(CaseService);

  caseNumber = '';
  title = '';
  description = '';

  readonly submitting = signal(false);
  readonly errorMessage = signal<string | null>(null);

  close() {
    if (this.submitting()) return;
    this.closed.emit();
  }

  submit() {
    if (this.submitting()) return;
    if (!this.caseNumber.trim() || !this.title.trim()) {
      this.errorMessage.set('Case number and title are required.');
      return;
    }

    this.submitting.set(true);
    this.errorMessage.set(null);

    this.caseService
      .create({
        case_number: this.caseNumber.trim(),
        title: this.title.trim(),
        description: this.description.trim() || undefined,
      })
      .subscribe({
        next: (detail) => {
          this.submitting.set(false);
          this.created.emit(detail);
        },
        error: (err: ApiError) => {
          this.submitting.set(false);
          this.errorMessage.set(err.message);
        },
      });
  }
}
