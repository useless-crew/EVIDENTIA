import { Component, OnDestroy, OnInit, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { ActivatedRoute, Router } from '@angular/router';
import { CaseService } from '../../core/services/case.service';
import { DocumentService } from '../../core/services/document.service';
import { ApiError } from '../../core/services/api-client.service';
import { DocumentSummary, RedactRegion, RedactionSummary } from '../../core/models/api.models';

/** Formats this real redaction pipeline can actually remove content from
 * (backend/internal/service/document_redact.go's supportedRedactionFormats
 * — kept in sync manually, since there is no endpoint that reports this
 * list). Any other mime_type is refused server-side with 422; the UI
 * checks this up front only to avoid a doomed round-trip and to show a
 * clear message instead of a bare drawing canvas over nothing. */
const SUPPORTED_MIME_TYPES = new Set(['image/png', 'image/jpeg']);

/** One region the user has drawn, kept in BOTH coordinate spaces: display
 * (CSS pixels, for rendering the mask over the <img>) and image (the
 * source image's own natural pixel space, for the actual API request —
 * the backend validates/applies regions against real image dimensions,
 * never rendered/zoomed ones). */
interface DrawnRegion {
  displayX: number;
  displayY: number;
  displayW: number;
  displayH: number;
  imageX: number;
  imageY: number;
  imageW: number;
  imageH: number;
}

@Component({
  selector: 'app-redact-studio',
  standalone: true,
  imports: [CommonModule],
  templateUrl: './redact-studio.component.html',
  styleUrls: ['./redact-studio.component.css'],
})
export class RedactStudioComponent implements OnInit, OnDestroy {
  private readonly caseService = inject(CaseService);
  private readonly documentService = inject(DocumentService);
  private readonly route = inject(ActivatedRoute);
  private readonly router = inject(Router);

  caseId = '';
  documentId = '';

  readonly loading = signal(true);
  readonly errorMessage = signal<string | null>(null);
  readonly doc = signal<DocumentSummary | null>(null);

  readonly imageLoading = signal(false);
  readonly imageUrl = signal<string | null>(null);
  readonly imageError = signal<string | null>(null);
  private naturalWidth = 0;
  private naturalHeight = 0;
  private renderedWidth = 0;
  private renderedHeight = 0;

  readonly reason = signal('');
  readonly regions = signal<DrawnRegion[]>([]);
  readonly draft = signal<{ x: number; y: number; w: number; h: number } | null>(null);

  readonly submitting = signal(false);
  readonly submitError = signal<string | null>(null);
  readonly result = signal<RedactionSummary | null>(null);

  private canvasBox: DOMRect | null = null;
  private isDrawing = false;
  private objectUrlToRevoke: string | null = null;

  get unsupported(): boolean {
    const d = this.doc();
    return d !== null && !SUPPORTED_MIME_TYPES.has(d.mime_type);
  }

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

  /** No standalone GET /documents/:id exists — the only source of a
   * document's metadata is the embedded documents[] array on
   * GET /cases/:id, same as DocumentViewerComponent.fetch(). Once we know
   * the mime_type is one this pipeline can actually redact, the real
   * bytes are downloaded so the user draws regions over the ACTUAL
   * evidence image, never a placeholder. */
  fetch() {
    this.loading.set(true);
    this.errorMessage.set(null);
    this.caseService.get(this.caseId).subscribe({
      next: (c) => {
        const found = c.documents.find((d) => d.id === this.documentId) ?? null;
        this.doc.set(found);
        this.loading.set(false);
        if (!found) {
          this.errorMessage.set('This document could not be found in the case record.');
          return;
        }
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
        this.imageLoading.set(false);
        const url = URL.createObjectURL(blob);
        this.objectUrlToRevoke = url;
        this.imageUrl.set(url);
      },
      error: (err: ApiError) => {
        this.imageLoading.set(false);
        this.imageError.set(err.message);
      },
    });
  }

  onImageLoad(img: HTMLImageElement) {
    this.naturalWidth = img.naturalWidth;
    this.naturalHeight = img.naturalHeight;
    const box = img.getBoundingClientRect();
    this.renderedWidth = box.width;
    this.renderedHeight = box.height;
  }

  private draftOrigin: { x: number; y: number } | null = null;

  onMouseDown(e: MouseEvent) {
    if (this.result()) return; // a derivative was already created — this canvas is done
    const el = e.currentTarget as HTMLElement;
    this.canvasBox = el.getBoundingClientRect();
    const x = e.clientX - this.canvasBox.left;
    const y = e.clientY - this.canvasBox.top;
    this.isDrawing = true;
    this.draftOrigin = { x, y };
    this.draft.set({ x, y, w: 0, h: 0 });
  }

  onMouseMove(e: MouseEvent) {
    if (!this.isDrawing || !this.canvasBox || !this.draftOrigin) return;
    const currentX = e.clientX - this.canvasBox.left;
    const currentY = e.clientY - this.canvasBox.top;
    const x = Math.min(currentX, this.draftOrigin.x);
    const y = Math.min(currentY, this.draftOrigin.y);
    const w = Math.abs(currentX - this.draftOrigin.x);
    const h = Math.abs(currentY - this.draftOrigin.y);
    this.draft.set({ x, y, w, h });
  }

  onMouseUp() {
    if (!this.isDrawing) return;
    this.isDrawing = false;
    const d = this.draft();
    this.draft.set(null);
    this.draftOrigin = null;
    if (!d || d.w < 8 || d.h < 8 || !this.naturalWidth || !this.renderedWidth) return;

    const scaleX = this.naturalWidth / this.renderedWidth;
    const scaleY = this.naturalHeight / this.renderedHeight;
    this.regions.update((list) => [
      ...list,
      {
        displayX: d.x,
        displayY: d.y,
        displayW: d.w,
        displayH: d.h,
        imageX: Math.round(d.x * scaleX),
        imageY: Math.round(d.y * scaleY),
        imageW: Math.round(d.w * scaleX),
        imageH: Math.round(d.h * scaleY),
      },
    ]);
  }

  removeRegion(index: number) {
    this.regions.update((list) => list.filter((_, i) => i !== index));
  }

  onReasonInput(value: string) {
    this.reason.set(value);
  }

  submit() {
    if (this.submitting()) return;
    const reason = this.reason().trim();
    const regions = this.regions();
    if (!reason || regions.length === 0) {
      this.submitError.set('Enter a reason and draw at least one region before saving.');
      return;
    }

    const payload: RedactRegion[] = regions.map((r) => ({
      page: 1,
      x: r.imageX,
      y: r.imageY,
      width: r.imageW,
      height: r.imageH,
    }));

    this.submitting.set(true);
    this.submitError.set(null);
    this.documentService.redact(this.documentId, reason, payload).subscribe({
      next: (summary) => {
        this.submitting.set(false);
        this.result.set(summary);
      },
      error: (err: ApiError) => {
        this.submitting.set(false);
        this.submitError.set(this.friendlyError(err));
      },
    });
  }

  private friendlyError(err: ApiError): string {
    switch (err.status) {
      case 401:
        return 'Your session has expired. Please sign in again.';
      case 403:
        return 'You do not have permission to redact this document.';
      case 404:
        return 'This document could not be found.';
      case 409:
        return 'This document failed integrity verification and cannot be redacted.';
      case 422:
        return err.message || 'Redaction is not supported for this file type.';
      default:
        return err.message || 'Redaction failed. Please try again.';
    }
  }

  viewDerivative() {
    const r = this.result();
    if (!r) return;
    this.router.navigate(['/app/cases', this.caseId, 'documents', r.document.id]);
  }

  cancel() {
    if (this.caseId && this.documentId) {
      this.router.navigate(['/app/cases', this.caseId, 'documents', this.documentId]);
    } else {
      this.router.navigateByUrl('/app/cases');
    }
  }

  ngOnDestroy() {
    if (this.objectUrlToRevoke) URL.revokeObjectURL(this.objectUrlToRevoke);
  }
}
