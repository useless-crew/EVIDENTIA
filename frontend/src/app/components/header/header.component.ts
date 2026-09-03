import { Component, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { DmsStateService } from '../../core/services/dms-state.service';

@Component({
  selector: 'app-header',
  standalone: true,
  imports: [CommonModule],
  templateUrl: './header.component.html',
  styleUrls: ['./header.component.css']
})
export class HeaderComponent {
  dms = inject(DmsStateService);

  signOut() {
    this.dms.signOut();
  }
}
