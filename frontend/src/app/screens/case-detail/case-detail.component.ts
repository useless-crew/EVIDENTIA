import { Component, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { DmsStateService, DocItem } from '../../core/services/dms-state.service';

@Component({
  selector: 'app-case-detail',
  standalone: true,
  imports: [CommonModule],
  templateUrl: './case-detail.component.html',
  styleUrls: ['./case-detail.component.css']
})
export class CaseDetailComponent {
  dms = inject(DmsStateService);
  viewMode: 'grid' | 'list' = 'grid';

  openDoc(d: DocItem) {
    this.dms.navigateTo('doc');
  }

  openUpload() {
    this.dms.openUploadModal();
  }

  goToAccess() {
    this.dms.navigateTo('access');
  }

  getDocBadgeClass(badgeType: string): string {
    switch (badgeType) {
      case 'verified': return 'badge-chargesheet';
      case 'pending': return 'badge-investigation';
      case 'redacted': return 'badge-trial';
      case 'mismatch': return 'badge-danger';
      default: return 'badge-closed';
    }
  }

  getDotColor(badgeType: string): string {
    switch (badgeType) {
      case 'verified': return '#2e7d4f';
      case 'pending': return '#b26a00';
      case 'redacted': return '#426d9b';
      case 'mismatch': return '#c53030';
      default: return '#5b6775';
    }
  }
}
