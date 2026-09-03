import { Component, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { DmsStateService } from '../../core/services/dms-state.service';
import { DocumentType } from '../../core/models/api.models';

@Component({
  selector: 'app-upload-modal',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './upload-modal.component.html',
  styleUrls: ['./upload-modal.component.css']
})
export class UploadModalComponent {
  dms = inject(DmsStateService);
  copied = false;

  readonly documentTypes: { value: DocumentType; label: string }[] = [
    { value: 'FIR', label: 'FIR' },
    { value: 'FORENSIC_REPORT', label: 'Forensic Report' },
    { value: 'PHOTO_EVIDENCE', label: 'Photo Evidence' },
    { value: 'WITNESS_STATEMENT', label: 'Witness Statement' },
    { value: 'OTHER', label: 'Other' },
  ];

  documentType: DocumentType = 'OTHER';
  description = '';
  selectedFile = signal<File | null>(null);
  readonly validationError = signal<string | null>(null);
  isDragOver = false;

  close() {
    this.dms.closeUploadModal();
  }

  onFileSelected(event: Event) {
    const input = event.target as HTMLInputElement;
    if (input.files && input.files.length > 0) {
      this.selectedFile.set(input.files[0]);
      this.validationError.set(null);
    }
  }

  onDrop(event: DragEvent) {
    event.preventDefault();
    this.isDragOver = false;
    const file = event.dataTransfer?.files?.[0];
    if (file) {
      this.selectedFile.set(file);
      this.validationError.set(null);
    }
  }

  onDragOver(event: DragEvent) {
    event.preventDefault();
    this.isDragOver = true;
  }

  onDragLeave() {
    this.isDragOver = false;
  }

  formatBytes(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }

  startUpload() {
    const file = this.selectedFile();
    if (!file) {
      this.validationError.set('Select a file to upload.');
      return;
    }
    this.dms.startUpload(file, this.documentType, this.description);
  }

  copyHash() {
    const hash = this.dms.liveHash();
    if (hash && navigator.clipboard) {
      navigator.clipboard.writeText(hash);
      this.copied = true;
      setTimeout(() => {
        this.copied = false;
      }, 2000);
    }
  }
}
