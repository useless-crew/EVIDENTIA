import { Component, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { DmsStateService } from '../../core/services/dms-state.service';

@Component({
  selector: 'app-login',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './login.component.html',
  styleUrls: ['./login.component.css']
})
export class LoginComponent {
  dms = inject(DmsStateService);

  email = 'r.mehra@delhipolice.gov.in';
  password = '••••••••••••';

  signIn() {
    this.dms.screen.set('dash');
  }
}
