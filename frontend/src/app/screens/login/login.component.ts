import { Component, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute, Router } from '@angular/router';
import { AuthService } from '../../core/services/auth.service';
import { ApiError } from '../../core/services/api-client.service';
import { Role } from '../../core/models/api.models';

/** Local-development convenience only — prefills the sign-in form with
 * one of the accounts backend/cmd/devuser created (see frontend/README.md
 * and backend/cmd/devuser/main.go's doc comment). This is NOT a bypass:
 * clicking one still submits the real credentials through signIn() below,
 * which calls the actual POST /auth/login — there is no code path here
 * that fabricates a session. These are dev-only accounts with no
 * production meaning; nothing here is a real government credential. */
interface DemoAccount {
  name: string;
  email: string;
  password: string;
  role: Role;
}

const DEMO_ACCOUNTS: DemoAccount[] = [
  { name: 'SI Rajat Mehra', email: 'police@delhipolice.gov.in', password: 'police123', role: 'POLICE' },
  { name: 'Hon. K. Mahadevan', email: 'judge@ecourts.gov.in', password: 'judge12345', role: 'JUDGE' },
  { name: 'Shalini Bhat', email: 'lawyer@prosecution.gov.in', password: 'lawyer1234', role: 'LAWYER' },
  { name: 'Dr. Anjali Iyer', email: 'forensics@cyberlab.gov.in', password: 'forensic123', role: 'FORENSICS' },
  { name: 'Nikhil Rao', email: 'admin@ncrb.gov.in', password: 'admin1234', role: 'ADMIN' },
];

@Component({
  selector: 'app-login',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './login.component.html',
  styleUrls: ['./login.component.css']
})
export class LoginComponent {
  private readonly auth = inject(AuthService);
  private readonly router = inject(Router);
  private readonly route = inject(ActivatedRoute);

  readonly demoAccounts = DEMO_ACCOUNTS;
  selectedAccount: DemoAccount = DEMO_ACCOUNTS[0];

  email = '';
  password = '';

  readonly submitting = signal(false);
  readonly errorMessage = signal<string | null>(null);
  readonly sessionExpired = signal(false);

  constructor() {
    this.sessionExpired.set(this.route.snapshot.queryParamMap.get('sessionExpired') === '1');
  }

  selectAccount(acc: DemoAccount) {
    this.selectedAccount = acc;
    this.email = acc.email;
    this.password = acc.password;
  }

  /** Prefills AND submits — still a real POST /auth/login call (see
   * signIn()), just a one-click convenience for local demo accounts. */
  quickSignIn(acc: DemoAccount, event?: Event) {
    if (event) event.preventDefault();
    this.selectAccount(acc);
    this.signIn();
  }

  signIn() {
    if (this.submitting()) return;
    this.errorMessage.set(null);
    this.submitting.set(true);

    this.auth.login(this.email.trim(), this.password).subscribe({
      next: () => {
        this.submitting.set(false);
        const redirectTo = this.route.snapshot.queryParamMap.get('redirectTo');
        this.router.navigateByUrl(redirectTo && redirectTo.startsWith('/app') ? redirectTo : '/app/dashboard');
      },
      error: (err: ApiError) => {
        this.submitting.set(false);
        this.errorMessage.set(err.message);
      }
    });
  }

  goToLanding(event?: Event) {
    if (event) event.preventDefault();
    this.router.navigateByUrl('/landing');
  }
}
