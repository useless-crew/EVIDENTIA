import { Component, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { DmsStateService, AuditRow } from '../../core/services/dms-state.service';

@Component({
  selector: 'app-audit-log',
  standalone: true,
  imports: [CommonModule],
  templateUrl: './audit-log.component.html',
  styleUrls: ['./audit-log.component.css']
})
export class AuditLogComponent {
  dms = inject(DmsStateService);

  verifyChain() {
    this.dms.verifyChain();
  }

  setTab(tab: 'table' | 'graph') {
    this.dms.auditTab.set(tab);
  }

  toggleRow(id: number) {
    this.dms.toggleAuditRow(id);
  }

  getActionBadgeClass(actionType: string): string {
    switch (actionType) {
      case 'upload': return 'action-upload';
      case 'hash': return 'action-hash';
      case 'status': return 'action-status';
      case 'view': return 'action-view';
      case 'redact': return 'action-redact';
      case 'denied': return 'action-denied';
      case 'verify': return 'action-verify';
      default: return 'action-default';
    }
  }
}
