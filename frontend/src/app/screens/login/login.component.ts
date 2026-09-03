import { Component, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute, Router } from '@angular/router';
import { AuthService } from '../../core/services/auth.service';
import { ApiError } from '../../core/services/api-client.service';
import { environment } from '../../../environments/environment';
import { DemoAccount } from './demo-accounts';

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

  readonly demoMode = environment.demoMode;
  readonly demoAccounts = signal<DemoAccount[]>([]);
  selectedAccount: DemoAccount | null = null;

  email = '';
  password = '';

  readonly submitting = signal(false);
  readonly errorMessage = signal<string | null>(null);
  readonly sessionExpired = signal(false);

  constructor() {
    this.sessionExpired.set(this.route.snapshot.queryParamMap.get('sessionExpired') === '1');

    // Dynamically imported so this module — and the dev-only credential
    // strings in it — is a separate chunk a production build (demoMode
    // false) never imports and the browser never fetches, not merely a
    // hidden UI element still bundled into the main chunk.
    if (this.demoMode) {
      import('./demo-accounts').then((m) => this.demoAccounts.set(m.DEMO_ACCOUNTS));
    }
  }

  selectAccount(acc: DemoAccount) {
    this.selectedAccount = acc;
    this.email = acc.email;
    this.password = acc.password;
  }

  /** Prefills AND submits — still a real POST /auth/login call (see
   * signIn()), just a one-click convenience for local demo accounts.
   * Unreachable in a production build: only rendered when demoMode is
   * true (see login.component.html). */
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
