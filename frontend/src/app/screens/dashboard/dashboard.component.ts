import { Component, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { DmsStateService } from '../../core/services/dms-state.service';

@Component({
  selector: 'app-dashboard',
  standalone: true,
  imports: [CommonModule],
  templateUrl: './dashboard.component.html',
  styleUrls: ['./dashboard.component.css']
})
export class DashboardComponent {
  dms = inject(DmsStateService);

  openUpload() {
    this.dms.openUploadModal();
  }

  goToCases() {
    this.dms.navigateTo('cases');
  }

  goToCase() {
    this.dms.navigateTo('case');
  }

  goToAudit() {
    this.dms.navigateTo('audit');
  }

  goToAccess() {
    this.dms.navigateTo('access');
  }
}
