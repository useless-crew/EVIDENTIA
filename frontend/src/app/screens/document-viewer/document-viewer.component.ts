import { Component, OnInit, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { ActivatedRoute, Router } from '@angular/router';
import { DmsStateService } from '../../core/services/dms-state.service';
import { CaseService } from '../../core/services/case.service';
import { DocumentService } from '../../core/services/document.service';
import { ApiError } from '../../core/services/api-client.service';
import { CertificateSummary, DocumentSummary, VerificationResult } from '../../core/models/api.models';

@Component({
  selector: 'app-document-viewer',
  standalone: true,
  imports: [CommonModule],
  templateUrl: './document-viewer.component.html',
  styleUrls: ['./document-viewer.component.css']
})
export class DocumentViewerComponent implements OnInit {
  dms = inject(DmsStateService);
  private readonly caseService = inject(CaseService);
  private readonly documentService = inject(DocumentService);
  private readonly route = inject(ActivatedRoute);
  private readonly router = inject(Router);

  caseId = '';
  documentId = '';

  readonly loading = signal(true);
  readonly errorMessage = signal<string | null>(null);
  readonly doc = signal<DocumentSummary | null>(null);

  zoomLevel = 100;

  // ---- POST /documents/:id/verify (System 7) ----
  readonly verifying = signal(false);
  readonly verifyResult = signal<VerificationResult | null>(null);
  readonly verifyError = signal<string | null>(null);

  // ---- GET /documents/:id/certificate (System 7) ----
  readonly certOpen = signal(false);
  readonly certLoading = signal(false);
  readonly certificate = signal<CertificateSummary | null>(null);
  readonly certNotFound = signal(false);
  readonly certError = signal<string | null>(null);
  private certFetched = false;

  // ---- GET /documents/:id/download (System 6) ----
  readonly downloading = signal(false);
  readonly downloadError = signal<string | null>(null);

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

  /** No standalone GET /documents/:id exists yet — the only source of a
   * document's metadata (filename, mime_type, uploaded_by, ...) is the
   * embedded documents[] array on GET /cases/:id (see
   * docs/API_ENDPOINTS.md's "Not yet implemented" note under Documents).
   * Verify/certificate/download below all call their own real,
   * document-scoped endpoints directly — only the display metadata comes
   * from the case fetch. */
  fetch() {
    this.loading.set(true);
    this.errorMessage.set(null);
    this.caseService.get(this.caseId).subscribe({
      next: (c) => {
        const found = c.documents.find((d) => d.id === this.documentId) ?? null;
        this.doc.set(found);
        if (!found) {
          this.errorMessage.set('This document could not be found in the case record.');
        }
        this.loading.set(false);
      },
      error: (err: ApiError) => {
        this.errorMessage.set(err.message);
        this.loading.set(false);
      },
    });
  }

  zoomIn() {
    if (this.zoomLevel < 150) this.zoomLevel += 10;
  }

  zoomOut() {
    if (this.zoomLevel > 70) this.zoomLevel -= 10;
  }

  resetZoom() {
    this.zoomLevel = 100;
  }

  /** Recomputes the SHA-256 of the actual stored object server-side and
   * compares it to the canonical hash — see
   * docs/SECURITY.md's "Document Verification & Compliance Certificates".
   * Both VERIFIED and INTEGRITY_FAILURE resolve this call successfully
   * (verifyResult is set either way); only a genuine storage/auth failure
   * lands in verifyError. Never confuse the two in the UI. */
  verify() {
    if (this.verifying()) return;
    this.verifying.set(true);
    this.verifyError.set(null);
    this.documentService.verify(this.documentId).subscribe({
      next: (result) => {
        this.verifying.set(false);
        this.verifyResult.set(result);
        // A tamper detection here also updates documents.status
        // server-side (TAMPERED/ACTIVE) — reflect that locally without a
        // full refetch.
        const current = this.doc();
        if (current) {
          this.doc.set({ ...current, status: result.status === 'INTEGRITY_FAILURE' ? 'TAMPERED' : 'ACTIVE' });
        }
      },
      error: (err: ApiError) => {
        this.verifying.set(false);
        this.verifyError.set(err.message);
      },
    });
  }

  /** Retrieves the compliance certificate — or generates one on demand if
   * the caller holds certificate:create and none exists yet (see
   * DocumentService.getCertificate's doc comment). A 404 means "not
   * issued" (never fabricated as if the document were unverified/
   * verified); a 409 means the document failed integrity verification. */
  toggleCert() {
    this.certOpen.set(!this.certOpen());
    if (this.certOpen() && !this.certFetched) {
      this.loadCertificate();
    }
  }

  loadCertificate() {
    this.certLoading.set(true);
    this.certError.set(null);
    this.certNotFound.set(false);
    this.documentService.getCertificate(this.documentId).subscribe({
      next: (cert) => {
        this.certFetched = true;
        this.certLoading.set(false);
        this.certificate.set(cert);
      },
      error: (err: ApiError) => {
        this.certFetched = true;
        this.certLoading.set(false);
        if (err.status === 404) {
          this.certNotFound.set(true);
        } else {
          this.certError.set(err.message);
        }
      },
    });
  }

  /** Streams the object through the backend (the authorization gateway —
   * never a MinIO URL) and saves it via a client-side object URL. */
  download() {
    if (this.downloading()) return;
    this.downloading.set(true);
    this.downloadError.set(null);
    this.documentService.download(this.documentId).subscribe({
      next: ({ blob, filename }) => {
        this.downloading.set(false);
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = filename;
        a.click();
        URL.revokeObjectURL(url);
      },
      error: (err: ApiError) => {
        this.downloading.set(false);
        this.downloadError.set(err.message);
      },
    });
  }

  goToRedact() {
    this.router.navigate(['/app/cases', this.caseId, 'documents', this.documentId, 'redact']);
  }
}
