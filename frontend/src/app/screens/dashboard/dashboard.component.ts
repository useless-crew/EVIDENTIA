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
    // No case context from the dashboard's generic shortcut — send the
    // user to pick a case, exactly like DmsStateService.navigateTo('upload')
    // does for the sidebar's equivalent nav item; a specific case's own
    // "Upload Document" button (CaseDetailComponent) opens the modal
    // directly with that case's id.
    this.dms.navigateTo('cases');
  }

  goToCases() {
    this.dms.navigateTo('cases');
  }

  goToCase() {
    this.dms.navigateTo('cases');
  }

  goToAudit() {
    this.dms.navigateTo('audit');
  }

  goToAccess() {
    this.dms.navigateTo('access');
  }
}
