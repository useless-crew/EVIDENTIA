import { Component, OnInit, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { Router } from '@angular/router';
import { DmsStateService } from '../../core/services/dms-state.service';
import { ShareService } from '../../core/services/share.service';
import { ApiError } from '../../core/services/api-client.service';
import { SharedWithMeResult } from '../../core/models/api.models';

const PAGE_SIZE = 20;

/**
 * "Shared With Me" (master prompt §59): every document for which the
 * caller currently holds an active, unexpired share — a real
 * GET /shared/documents call, never case-membership-derived. This is the
 * ONLY place a share recipient with no relationship to a document's case
 * can discover it at all (see document.service.ts/api-client.service.ts —
 * there is no standalone GET /documents/:id).
 */
@Component({
  selector: 'app-shared-with-me',
  standalone: true,
  imports: [CommonModule],
  templateUrl: './shared-with-me.component.html',
  styleUrls: ['./shared-with-me.component.css'],
})
export class SharedWithMeComponent implements OnInit {
  dms = inject(DmsStateService);
  private readonly shareService = inject(ShareService);
  private readonly router = inject(Router);

  readonly loading = signal(true);
  readonly errorMessage = signal<string | null>(null);
  readonly result = signal<SharedWithMeResult | null>(null);
  readonly page = signal(1);

  ngOnInit() {
    this.fetch();
  }

  fetch() {
    this.loading.set(true);
    this.errorMessage.set(null);
    this.shareService.sharedWithMe(this.page(), PAGE_SIZE).subscribe({
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

  goToPage(p: number) {
    const meta = this.result()?.meta;
    if (!meta || p < 1 || p > meta.total_pages) return;
    this.page.set(p);
    this.fetch();
  }

  openDocument(caseId: string, documentId: string) {
    this.router.navigate(['/app/cases', caseId, 'documents', documentId]);
  }

  formatType(type: string): string {
    return type.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
  }
}
