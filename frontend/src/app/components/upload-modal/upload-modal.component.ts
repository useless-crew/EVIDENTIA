import { Component, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { DmsStateService } from '../../core/services/dms-state.service';

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

  close() {
    this.dms.closeUploadModal();
  }

  startUpload() {
    this.dms.startUploadProcess();
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
