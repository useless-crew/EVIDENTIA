import { Component, OnDestroy, OnInit, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute, Router } from '@angular/router';
import { CaseService } from '../../core/services/case.service';
import { DocumentService } from '../../core/services/document.service';
import { ApiError } from '../../core/services/api-client.service';
import { DocumentSummary, RedactRegion, RedactionSummary } from '../../core/models/api.models';

/** One masked rectangle, in the SOURCE image's own pixel coordinate space —
 * the redact canvas renders the real document image at its natural size
 * (no CSS scaling), so on-screen mouse coordinates ARE image pixel
 * coordinates directly; no scale-factor conversion is needed. */
interface DraftRegion {
  id: number;
  x: number;
  y: number;
  width: number;
  height: number;
}

const SUPPORTED_MIME_TYPES = new Set(['image/png', 'image/jpeg']);

/**
 * System 8 (redaction) — real integration. Loads the actual source
 * document image (GET /documents/:id/download), lets the user draw
 * rectangular masks over it in the image's own pixel space, and submits
 * them via POST /documents/:id/redact (DocumentService.redact). The
 * backend performs the real, destructive pixel removal and returns a
 * brand-new, independently-hashed derivative document — the source
 * document is never modified. Only image/png and image/jpeg documents can
 * be redacted; any other format shows a clear, non-fakeable error instead
 * of a canvas (see backend/internal/service/document_redact.go).
 */
@Component({
  selector: 'app-redact-studio',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './redact-studio.component.html',
  styleUrls: ['./redact-studio.component.css']
})
export class RedactStudioComponent implements OnInit, OnDestroy {
  private readonly route = inject(ActivatedRoute);
  private readonly router = inject(Router);
  private readonly caseService = inject(CaseService);
  private readonly documentService = inject(DocumentService);

  caseId = '';
  documentId = '';

  readonly loading = signal(true);
  readonly errorMessage = signal<string | null>(null);
  readonly doc = signal<DocumentSummary | null>(null);

  readonly imageLoading = signal(false);
  readonly imageError = signal<string | null>(null);
  readonly imageUrl = signal<string | null>(null);
  private objectUrl: string | null = null;

  readonly regions = signal<DraftRegion[]>([]);
  readonly draft = signal<{ x: number; y: number; w: number; h: number } | null>(null);
  reason = '';

  readonly submitting = signal(false);
  readonly submitError = signal<string | null>(null);
  readonly result = signal<RedactionSummary | null>(null);

  private canvasBox: DOMRect | null = null;
  private isDrawing = false;
  private nextRegionId = 1;

  ngOnInit() {
    this.route.paramMap.subscribe((params) => {
      const caseId = params.get('caseId');
      const documentId = params.get('documentId');
      if (caseId && documentId) {
        this.caseId = caseId;
        this.documentId = documentId;
        this.fetch();
      }
    });
  }

  ngOnDestroy() {
    this.releaseObjectUrl();
  }

  get isSupportedFormat(): boolean {
    const d = this.doc();
    return !!d && SUPPORTED_MIME_TYPES.has(d.mime_type);
  }

  fetch() {
    this.loading.set(true);
    this.errorMessage.set(null);
    this.caseService.get(this.caseId).subscribe({
      next: (c) => {
        const found = c.documents.find((d) => d.id === this.documentId) ?? null;
        if (!found) {
          this.errorMessage.set('This document could not be found in the case record.');
          this.loading.set(false);
          return;
        }
        this.doc.set(found);
        this.loading.set(false);
        if (SUPPORTED_MIME_TYPES.has(found.mime_type)) {
          this.loadImage();
        }
      },
      error: (err: ApiError) => {
        this.errorMessage.set(err.message);
        this.loading.set(false);
      },
    });
  }

  private loadImage() {
    this.imageLoading.set(true);
    this.imageError.set(null);
    this.documentService.download(this.documentId).subscribe({
      next: ({ blob }) => {
        this.releaseObjectUrl();
        this.objectUrl = URL.createObjectURL(blob);
        this.imageUrl.set(this.objectUrl);
        this.imageLoading.set(false);
      },
      error: (err: ApiError) => {
        this.imageError.set(err.message);
        this.imageLoading.set(false);
      },
    });
  }

  private releaseObjectUrl() {
    if (this.objectUrl) {
      URL.revokeObjectURL(this.objectUrl);
      this.objectUrl = null;
    }
  }

  private dragOrigin = { x: 0, y: 0 };

  onMouseDown(e: MouseEvent) {
    if (this.result()) return;
    const el = e.currentTarget as HTMLElement;
    this.canvasBox = el.getBoundingClientRect();
    const x = e.clientX - this.canvasBox.left;
    const y = e.clientY - this.canvasBox.top;
    this.dragOrigin = { x, y };
    this.isDrawing = true;
    this.draft.set({ x, y, w: 0, h: 0 });
  }

  onMouseMove(e: MouseEvent) {
    if (!this.isDrawing || !this.canvasBox) return;
    const currentX = e.clientX - this.canvasBox.left;
    const currentY = e.clientY - this.canvasBox.top;
    const x = Math.min(currentX, this.dragOrigin.x);
    const y = Math.min(currentY, this.dragOrigin.y);
    const w = Math.abs(currentX - this.dragOrigin.x);
    const h = Math.abs(currentY - this.dragOrigin.y);
    this.draft.set({ x, y, w, h });
  }

  onMouseUp() {
    if (!this.isDrawing) return;
    this.isDrawing = false;
    const d = this.draft();
    if (d && d.w > 12 && d.h > 10) {
      this.regions.set([
        ...this.regions(),
        { id: this.nextRegionId++, x: d.x, y: d.y, width: d.w, height: d.h },
      ]);
    }
    this.draft.set(null);
  }

  removeRegion(id: number) {
    this.regions.set(this.regions().filter((r) => r.id !== id));
  }

  regionDims(r: DraftRegion): string {
    return `${Math.round(r.width)}×${Math.round(r.height)} px @ ${Math.round(r.x)},${Math.round(r.y)}`;
  }

  saveCopy() {
    if (this.submitting() || this.result()) return;

    if (this.regions().length === 0) {
      this.submitError.set('Draw at least one region to mask before saving.');
      return;
    }
    if (this.reason.trim().length < 3) {
      this.submitError.set('Provide a redaction reason (at least 3 characters).');
      return;
    }

    this.submitting.set(true);
    this.submitError.set(null);

    const regions: RedactRegion[] = this.regions().map((r) => ({
      page: 1,
      x: r.x,
      y: r.y,
      width: r.width,
      height: r.height,
    }));

    this.documentService.redact(this.documentId, this.reason.trim(), regions).subscribe({
      next: (summary) => {
        this.submitting.set(false);
        this.result.set(summary);
      },
      error: (err: ApiError) => {
        this.submitting.set(false);
        this.submitError.set(err.message);
      },
    });
  }

  viewDerivative() {
    const r = this.result();
    if (!r) return;
    this.router.navigate(['/app/cases', this.caseId, 'documents', r.document.id]);
  }

  cancel() {
    this.router.navigate(['/app/cases', this.caseId, 'documents', this.documentId]);
  }
}
