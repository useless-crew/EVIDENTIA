import { Injectable, inject } from '@angular/core';
import { Observable, map } from 'rxjs';
import {
  CertificateSummary,
  DocumentType,
  RedactRegion,
  RedactionSummary,
  UploadDocumentResponse,
  VerificationResult,
} from '../models/api.models';
import { ApiClientService, UploadEvent } from './api-client.service';

export interface DownloadedFile {
  blob: Blob;
  filename: string;
}

export interface UploadDocumentInput {
  documentType: DocumentType;
  description?: string;
  file: File;
}

/**
 * System 6 (upload/download) and System 7 (verify/certificate) document
 * endpoints — see docs/API_ENDPOINTS.md's "Case Documents"/"Documents"
 * sections. Every call here maps 1:1 to a real backend route; nothing in
 * this service fabricates a result.
 */
@Injectable({ providedIn: 'root' })
export class DocumentService {
  private readonly api = inject(ApiClientService);

  /**
   * POST /cases/:caseId/documents — multipart/form-data. Field order in
   * the body matters (internal/handlers/document/upload.go reads it as a
   * true stream, in the order sent): document_type and description (if
   * present) MUST be appended before file, exactly matching the order
   * FormData.append is called here — this is the actual mechanism that
   * satisfies that contract, not just a comment.
   */
  upload(caseId: string, input: UploadDocumentInput): Observable<UploadEvent<UploadDocumentResponse>> {
    const form = new FormData();
    form.append('document_type', input.documentType);
    if (input.description) form.append('description', input.description);
    form.append('file', input.file, input.file.name);

    return this.api.postMultipart<UploadDocumentResponse>(`/cases/${caseId}/documents`, form);
  }

  /** GET /documents/:id/download — streamed server-side, returned here as
   * a Blob plus the server-suggested filename (Content-Disposition,
   * exposed cross-origin specifically for this — see
   * internal/middleware/cors_middleware.go). Never a MinIO URL: this IS
   * the authorization gateway request. */
  download(documentId: string): Observable<DownloadedFile> {
    return this.api.getBlob(`/documents/${documentId}/download`).pipe(
      map((res) => ({
        blob: res.body as Blob,
        filename: this.filenameFromContentDisposition(res.headers.get('Content-Disposition')) ?? 'document',
      }))
    );
  }

  /** POST /documents/:id/verify — recomputes the SHA-256 of the actual
   * stored object and compares it to the canonical hash. Always resolves
   * on a completed verification (VERIFIED or INTEGRITY_FAILURE are both
   * a 200 — see VerificationResult's own doc comment); rejects only on a
   * genuine authorization/storage failure. */
  verify(documentId: string): Observable<VerificationResult> {
    return this.api.post<VerificationResult>(`/documents/${documentId}/verify`);
  }

  /** GET /documents/:id/certificate — returns the existing certificate,
   * or (for a caller who also holds certificate:create) generates one on
   * demand bound to the document's current hash. Rejects with a 404
   * ApiError if none exists and the caller cannot generate one, and a 409
   * ApiError if the document fails integrity verification. */
  getCertificate(documentId: string): Observable<CertificateSummary> {
    return this.api.get<CertificateSummary>(`/documents/${documentId}/certificate`);
  }

  /** POST /documents/:id/redact — produces a brand-new, independent
   * derivative document with the given regions' pixel content genuinely
   * removed (opaque black, never an overlay) for supported image formats
   * only (image/png, image/jpeg — a 422 ApiError otherwise). The backend
   * computes the derivative's hash; this method never touches hashing.
   * The original document is never modified by this call. */
  redact(documentId: string, reason: string, regions: RedactRegion[]): Observable<RedactionSummary> {
    return this.api.post<RedactionSummary>(`/documents/${documentId}/redact`, { reason, regions });
  }

  private filenameFromContentDisposition(header: string | null): string | null {
    if (!header) return null;
    const match = /filename="?([^";]+)"?/i.exec(header);
    return match ? match[1] : null;
  }
}
