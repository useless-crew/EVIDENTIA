import { Component, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { DmsStateService } from '../../core/services/dms-state.service';
import { RevealDirective } from '../../core/directives/reveal.directive';
import { CountUpDirective } from '../../core/directives/count-up.directive';

@Component({
  selector: 'app-dashboard',
  standalone: true,
  imports: [CommonModule, RevealDirective, CountUpDirective],
  templateUrl: './dashboard.component.html',
  styleUrls: ['./dashboard.component.css']
})
export class DashboardComponent {
  dms = inject(DmsStateService);

  /**
   * Dashboard stat values are display strings ('23', '1,204'). Strip grouping
   * and parse so the counter can animate; anything non-numeric renders as-is.
   */
  statNumber(value: string): number {
    return Number(String(value).replace(/[^0-9.-]/g, ''));
  }

  isNumericStat(value: string): boolean {
    const parsed = this.statNumber(value);
    return String(value).trim() !== '' && Number.isFinite(parsed);
  }

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

  goToShared() {
    this.dms.navigateTo('shared');
  }

  goToAudit() {
    this.dms.navigateTo('audit');
  }

  goToAccess() {
    this.dms.navigateTo('access');
  }
}
