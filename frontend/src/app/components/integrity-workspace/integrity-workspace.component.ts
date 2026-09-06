import { Component, EventEmitter, Input, OnInit, Output, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { Router } from '@angular/router';
import { DocumentService } from '../../core/services/document.service';
import { BlockchainAnchorSummary, IntegrityVerifyResult } from '../../core/models/api.models';
import { ApiError } from '../../core/services/api-client.service';

@Component({
  selector: 'app-integrity-workspace',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './integrity-workspace.component.html',
  styleUrls: ['./integrity-workspace.component.css'],
})
export class IntegrityWorkspaceComponent implements OnInit {
  private readonly documentService = inject(DocumentService);
  private readonly router = inject(Router);

  @Input({ required: true }) documentId!: string;
  @Input({ required: true }) documentFilename!: string;
  @Input({ required: true }) documentHash!: string;
  @Input() caseId?: string;
  @Input() documentVersion: number = 1;

  @Output() closed = new EventEmitter<void>();
  @Output() verified = new EventEmitter<IntegrityVerifyResult>();

  // State
  readonly sourceMode = signal<'stored' | 'candidate'>('candidate');
  readonly selectedFile = signal<File | Blob | null>(null);
  readonly selectedFileName = signal<string>('');
  readonly selectedFileSize = signal<number>(0);
  readonly isDragOver = signal<boolean>(false);

  readonly verifying = signal<boolean>(false);
  readonly verifyError = signal<string | null>(null);
  readonly result = signal<IntegrityVerifyResult | null>(null);

  readonly blockchainAnchor = signal<BlockchainAnchorSummary | null>(null);
  readonly provenanceHistory = signal<BlockchainAnchorSummary[] | null>(null);
  readonly loadingProvenance = signal<boolean>(false);
  readonly showProvenance = signal<boolean>(false);

  // Demo synthetic helper state
  readonly activeDemoPreset = signal<'original' | 'tampered' | null>(null);

  ngOnInit(): void {
    this.loadBlockchainAnchor();
  }

  loadBlockchainAnchor(): void {
    this.documentService.getBlockchainStatus(this.documentId).subscribe({
      next: (anchor) => {
        this.blockchainAnchor.set(anchor);
      },
      error: () => {
        // Safe: non-blocking, blockchain status may be unavailable
      },
    });
  }

  setSourceMode(mode: 'stored' | 'candidate'): void {
    this.sourceMode.set(mode);
    this.result.set(null);
    this.verifyError.set(null);
  }

  onFileSelected(event: Event): void {
    const input = event.target as HTMLInputElement;
    if (input.files && input.files.length > 0) {
      const file = input.files[0];
      this.selectedFile.set(file);
      this.selectedFileName.set(file.name);
      this.selectedFileSize.set(file.size);
      this.activeDemoPreset.set(null);
      this.result.set(null);
      this.verifyError.set(null);
    }
  }

  onDragOver(event: DragEvent): void {
    event.preventDefault();
    event.stopPropagation();
    this.isDragOver.set(true);
  }

  onDragLeave(): void {
    this.isDragOver.set(false);
  }

  onDrop(event: DragEvent): void {
    event.preventDefault();
    event.stopPropagation();
    this.isDragOver.set(false);
    if (event.dataTransfer?.files && event.dataTransfer.files.length > 0) {
      const file = event.dataTransfer.files[0];
      this.selectedFile.set(file);
      this.selectedFileName.set(file.name);
      this.selectedFileSize.set(file.size);
      this.activeDemoPreset.set(null);
      this.result.set(null);
      this.verifyError.set(null);
    }
  }

  /**
   * Loads safe, synthetic demonstration candidate data directly in-browser.
   * Clearly watermarked with DEMONSTRATION DATA per Master Prompt §20.
   */
  loadSyntheticDemo(type: 'original' | 'tampered'): void {
    this.activeDemoPreset.set(type);
    this.result.set(null);
    this.verifyError.set(null);

    let content: string;
    let filename: string;

    if (type === 'original') {
      filename = 'evidence_original.pdf';
      content = `%PDF-1.4
1 0 obj
<< /Type /Catalog /Pages 2 0 R >>
endobj
2 0 obj
<< /Type /Pages /Kids [3 0 R] /Count 1 >>
endobj
3 0 obj
<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>
endobj
4 0 obj
<< /Length 150 >>
stream
BT
/F1 14 Tf
50 700 Td
(DEMONSTRATION DATA - EVIDENTIA DIGITAL EVIDENCE SAMPLE 01) Tj
50 670 Td
(STATUS: OFFICIAL REGISTERED POLICE EVIDENCE - INTEGRITY UNTOUCHED) Tj
ET
endstream
endobj
5 0 obj
<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>
endobj
xref
0 6
0000000000 65535 f 
0000000009 00000 n 
0000000058 00000 n 
0000000115 00000 n 
0000000234 00000 n 
0000000436 00000 n 
trailer
<< /Size 6 /Root 1 0 R >>
startxref
505
%%EOF`;
    } else {
      filename = 'evidence_modified.pdf';
      content = `%PDF-1.4
1 0 obj
<< /Type /Catalog /Pages 2 0 R >>
endobj
2 0 obj
<< /Type /Pages /Kids [3 0 R] /Count 1 >>
endobj
3 0 obj
<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>
endobj
4 0 obj
<< /Length 150 >>
stream
BT
/F1 14 Tf
50 700 Td
(DEMONSTRATION DATA - EVIDENTIA DIGITAL EVIDENCE SAMPLE 01) Tj
50 670 Td
(STATUS: TAMPERED COPY - MODIFIED EVIDENCE CONTENT DETECTED!) Tj
ET
endstream
endobj
5 0 obj
<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>
endobj
xref
0 6
0000000000 65535 f 
0000000009 00000 n 
0000000058 00000 n 
0000000115 00000 n 
0000000234 00000 n 
0000000436 00000 n 
trailer
<< /Size 6 /Root 1 0 R >>
startxref
505
%%EOF`;
    }

    const blob = new Blob([content], { type: 'application/pdf' });
    this.selectedFile.set(blob);
    this.selectedFileName.set(filename);
    this.selectedFileSize.set(blob.size);
  }

  runVerification(): void {
    if (this.verifying()) return;

    if (this.sourceMode() === 'candidate' && !this.selectedFile()) {
      this.verifyError.set('Please select or drag-and-drop a verification candidate file, or select a demo preset.');
      return;
    }

    this.verifying.set(true);
    this.verifyError.set(null);

    const file = this.sourceMode() === 'candidate' ? this.selectedFile() ?? undefined : undefined;
    const filename = this.sourceMode() === 'candidate' ? this.selectedFileName() : undefined;

    this.documentService
      .verifyCandidate(this.documentId, file, filename, this.sourceMode())
      .subscribe({
        next: (res) => {
          this.verifying.set(false);
          this.result.set(res);
          this.verified.emit(res);
        },
        error: (err: ApiError) => {
          this.verifying.set(false);
          this.verifyError.set(err.message || 'Verification failed. Please try again.');
        },
      });
  }

  toggleProvenance(): void {
    this.showProvenance.set(!this.showProvenance());
    if (this.showProvenance() && !this.provenanceHistory()) {
      this.loadingProvenance.set(true);
      this.documentService.getBlockchainProvenance(this.documentId).subscribe({
        next: (history) => {
          this.provenanceHistory.set(history);
          this.loadingProvenance.set(false);
        },
        error: () => {
          this.loadingProvenance.set(false);
        },
      });
    }
  }

  goToAuditLog(): void {
    this.close();
    this.router.navigateByUrl('/app/audit');
  }

  resetCandidate(): void {
    this.selectedFile.set(null);
    this.selectedFileName.set('');
    this.selectedFileSize.set(0);
    this.activeDemoPreset.set(null);
    this.result.set(null);
    this.verifyError.set(null);
  }

  close(): void {
    this.closed.emit();
  }

  formatBytes(bytes: number): string {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`;
  }
}
