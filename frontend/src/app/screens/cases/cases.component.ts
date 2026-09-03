import { Component, OnInit, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { Router } from '@angular/router';
import { DmsStateService } from '../../core/services/dms-state.service';
import { CaseService } from '../../core/services/case.service';
import { ApiError } from '../../core/services/api-client.service';
import { CaseDetail, CaseListResult, CaseStatus, CaseSummary } from '../../core/models/api.models';
import { CreateCaseModalComponent } from '../../components/create-case-modal/create-case-modal.component';

const PAGE_SIZE = 20;

@Component({
  selector: 'app-cases',
  standalone: true,
  imports: [CommonModule, CreateCaseModalComponent],
  templateUrl: './cases.component.html',
  styleUrls: ['./cases.component.css']
})
export class CasesComponent implements OnInit {
  dms = inject(DmsStateService);
  private readonly caseService = inject(CaseService);
  private readonly router = inject(Router);

  readonly loading = signal(true);
  readonly errorMessage = signal<string | null>(null);
  readonly result = signal<CaseListResult | null>(null);
  readonly page = signal(1);
  readonly searchTerm = signal('');
  readonly statusFilter = signal<CaseStatus | ''>('');
  readonly createOpen = signal(false);

  readonly statuses: CaseStatus[] = ['OPEN', 'UNDER_INVESTIGATION', 'SUBMITTED', 'UNDER_REVIEW', 'CLOSED', 'ARCHIVED'];

  ngOnInit() {
    this.fetch();
  }

  fetch() {
    this.loading.set(true);
    this.errorMessage.set(null);
    this.caseService
      .list({
        page: this.page(),
        page_size: PAGE_SIZE,
        title: this.searchTerm().trim() || undefined,
        status: this.statusFilter() || undefined,
      })
      .subscribe({
        next: (res) => {
          this.result.set(res);
          this.loading.set(false);
        },
        error: (err: ApiError) => {
          this.errorMessage.set(err.message);
          this.loading.set(false);
        },
      });
  }

  onSearchInput(value: string) {
    this.searchTerm.set(value);
    this.page.set(1);
    this.fetch();
  }

  onStatusChange(value: string) {
    this.statusFilter.set(value as CaseStatus | '');
    this.page.set(1);
    this.fetch();
  }

  goToPage(p: number) {
    const meta = this.result()?.meta;
    if (!meta || p < 1 || p > meta.total_pages) return;
    this.page.set(p);
    this.fetch();
  }

  openCase(record: CaseSummary) {
    this.router.navigate(['/app/cases', record.id]);
  }

  openCreate() {
    this.createOpen.set(true);
  }

  onCaseCreated(detail: CaseDetail) {
    this.createOpen.set(false);
    this.router.navigate(['/app/cases', detail.id]);
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

  formatStatus(status: string): string {
    return status.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
  }
}
