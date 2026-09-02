import { Component, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { DmsStateService, CaseRecord } from '../../core/services/dms-state.service';

@Component({
  selector: 'app-cases',
  standalone: true,
  imports: [CommonModule],
  templateUrl: './cases.component.html',
  styleUrls: ['./cases.component.css']
})
export class CasesComponent {
  dms = inject(DmsStateService);

  openCase(record: CaseRecord) {
    this.dms.navigateTo('case');
  }

  getStatusClass(status: string): string {
    switch (status) {
      case 'Under investigation': return 'badge-investigation';
      case 'Chargesheet filed': return 'badge-chargesheet';
      case 'In trial': return 'badge-trial';
      case 'Closed': return 'badge-closed';
      default: return 'badge-closed';
    }
  }
}
