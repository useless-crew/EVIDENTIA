import { Component, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { DmsStateService, Screen } from '../../core/services/dms-state.service';

@Component({
  selector: 'app-sidebar',
  standalone: true,
  imports: [CommonModule],
  templateUrl: './sidebar.component.html',
  styleUrls: ['./sidebar.component.css']
})
export class SidebarComponent {
  dms = inject(DmsStateService);

  isItemActive(target: Screen | 'upload'): boolean {
    const current = this.dms.screen();
    if (target === current) return true;
    if (target === 'cases' && (current === 'case' || current === 'doc' || current === 'redact' || current === 'access')) {
      return true;
    }
    return false;
  }

  onNavClick(target: Screen | 'upload') {
    this.dms.navigateTo(target);
  }
}
