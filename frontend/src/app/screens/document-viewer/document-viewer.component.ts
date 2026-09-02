import { Component, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { DmsStateService, H_DOC } from '../../core/services/dms-state.service';

@Component({
  selector: 'app-document-viewer',
  standalone: true,
  imports: [CommonModule],
  templateUrl: './document-viewer.component.html',
  styleUrls: ['./document-viewer.component.css']
})
export class DocumentViewerComponent {
  dms = inject(DmsStateService);

  readonly docHash = H_DOC;
  readonly certHash = H_DOC.slice(0, 24) + '…';

  zoomLevel = 100;
  certOpen = false;

  zoomIn() {
    if (this.zoomLevel < 150) this.zoomLevel += 10;
  }

  zoomOut() {
    if (this.zoomLevel > 70) this.zoomLevel -= 10;
  }

  resetZoom() {
    this.zoomLevel = 100;
  }

  verify() {
    this.dms.verifyDocumentIntegrity();
  }

  goToRedact() {
    this.dms.navigateTo('redact');
  }

  toggleCert() {
    this.certOpen = !this.certOpen;
  }
}
