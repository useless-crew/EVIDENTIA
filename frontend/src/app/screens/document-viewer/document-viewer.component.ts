import { Component, OnInit, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { ActivatedRoute, Router } from '@angular/router';
import { DmsStateService } from '../../core/services/dms-state.service';
import { CaseService } from '../../core/services/case.service';
import { DocumentService } from '../../core/services/document.service';
import { ShareService } from '../../core/services/share.service';
import { ApiError } from '../../core/services/api-client.service';
import { CertificateSummary, DocumentSummary, ShareSummary, VerificationResult } from '../../core/models/api.models';
import { ShareDialogComponent } from '../../components/share-dialog/share-dialog.component';

@Component({
  selector: 'app-document-viewer',
  standalone: true,
  imports: [CommonModule, ShareDialogComponent],
  templateUrl: './document-viewer.component.html',
  styleUrls: ['./document-viewer.component.css']
})
export class DocumentViewerComponent implements OnInit {
  dms = inject(DmsStateService);
  private readonly caseService = inject(CaseService);
  private readonly documentService = inject(DocumentService);
  private readonly shareService = inject(ShareService);
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

  // ---- Document sharing ----
  // canManageShares reflects a REAL backend authorization result (whether
  // GET /documents/:id/shares succeeded), never a client-side guess — a
  // caller who cannot manage this document's sharing simply never sees
  // this section, and the backend enforces the same rule independently
  // regardless of what this signal shows (frontend visibility is not
  // security).
  readonly canManageShares = signal(false);
  readonly sharesLoading = signal(false);
  readonly shares = signal<ShareSummary[]>([]);
  readonly shareDialogOpen = signal(false);
  readonly revokingShareId = signal<string | null>(null);
  readonly shareActionError = signal<string | null>(null);

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

  /** No standalone GET /documents/:id exists yet — the primary source of
   * a document's metadata (filename, mime_type, uploaded_by, ...) is the
   * embedded documents[] array on GET /cases/:id (see
   * docs/API_ENDPOINTS.md's "Not yet implemented" note under Documents).
   * That call requires case membership, though — a share recipient with
   * NO relationship to the document's case (the whole point of sharing
   * with someone outside it) gets a 403 from it. In that case, fall back
   * to GET /shared/documents and find this exact document there instead
   * — the recipient's own real, backend-authorized access path (see
   * ShareService.sharedWithMe). Verify/certificate/download below all
   * call their own real, document-scoped endpoints directly regardless
   * of which path found the metadata. */
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
        this.loadShares();
        this.loading.set(false);
      },
      error: () => this.fetchViaSharedWithMe(),
    });
  }

  /** Fallback for a caller with no case relationship but an active share
   * on this exact document — see fetch()'s doc comment. */
  private fetchViaSharedWithMe() {
    this.shareService.sharedWithMe(1, 100).subscribe({
      next: (result) => {
        const match = result.documents.find((d) => d.document.id === this.documentId);
        if (match) {
          this.doc.set(match.document);
          // A pure recipient never manages this document's sharing —
          // GET /documents/:id/shares would 403 for them too; skip the
          // call entirely rather than surface an expected denial.
        } else {
          this.errorMessage.set('You do not have access to this document.');
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

  /** GET /documents/:id/shares — only the caller's own real authorization
   * result decides whether the sharing UI appears (see canManageShares's
   * doc comment). A 403 here is an expected, silent outcome for most
   * viewers, not an error to surface. */
  private loadShares() {
    this.sharesLoading.set(true);
    this.shareService.list(this.documentId).subscribe({
      next: (result) => {
        this.sharesLoading.set(false);
        this.canManageShares.set(true);
        this.shares.set(result.shares);
      },
      error: () => {
        this.sharesLoading.set(false);
        this.canManageShares.set(false);
        this.shares.set([]);
      },
    });
  }

  openShareDialog() {
    this.shareActionError.set(null);
    this.shareDialogOpen.set(true);
  }

  onShareDialogClosed() {
    this.shareDialogOpen.set(false);
  }

  onShared(_summary: ShareSummary) {
    this.shareDialogOpen.set(false);
    this.loadShares();
  }

  revokeShare(shareId: string) {
    if (this.revokingShareId()) return;
    if (!confirm('Revoke this share? The recipient will immediately lose access.')) return;

    this.revokingShareId.set(shareId);
    this.shareActionError.set(null);
    this.shareService.revoke(this.documentId, shareId).subscribe({
      next: () => {
        this.revokingShareId.set(null);
        this.loadShares();
      },
      error: (err: ApiError) => {
        this.revokingShareId.set(null);
        this.shareActionError.set(err.message);
      },
    });
  }
}
