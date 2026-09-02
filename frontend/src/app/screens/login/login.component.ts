import { Component, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { DmsStateService, DUMMY_ACCOUNTS, UserAccount } from '../../core/services/dms-state.service';

@Component({
  selector: 'app-login',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './login.component.html',
  styleUrls: ['./login.component.css']
})
export class LoginComponent {
  dms = inject(DmsStateService);
  dummyAccounts = DUMMY_ACCOUNTS;

  email = DUMMY_ACCOUNTS[0].email;
  password = DUMMY_ACCOUNTS[0].password;
  selectedAccount: UserAccount = DUMMY_ACCOUNTS[0];

  selectAccount(acc: UserAccount) {
    this.selectedAccount = acc;
    this.email = acc.email;
    this.password = acc.password;
  }

  quickSignIn(acc: UserAccount, event?: Event) {
    if (event) {
      event.preventDefault();
    }
    this.selectAccount(acc);
    this.dms.loginWithAccount(acc);
  }

  signIn() {
    this.dms.loginWithCredentials(this.email, this.password);
  }

  goToLanding(event?: Event) {
    if (event) {
      event.preventDefault();
    }
    this.dms.screen.set('landing');
  }
}


