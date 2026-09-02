import { Component, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { DmsStateService, Role } from '../../core/services/dms-state.service';

@Component({
  selector: 'app-header',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './header.component.html',
  styleUrls: ['./header.component.css']
})
export class HeaderComponent {
  dms = inject(DmsStateService);

  onRoleChange(event: Event) {
    const select = event.target as HTMLSelectElement;
    this.dms.setRole(select.value as Role);
  }

  toggleTamper() {
    this.dms.simulateTamper.set(!this.dms.simulateTamper());
  }

  signOut() {
    this.dms.screen.set('login');
  }
}
