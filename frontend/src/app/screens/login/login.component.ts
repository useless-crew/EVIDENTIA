import { Component, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute, Router } from '@angular/router';
import { AuthService } from '../../core/services/auth.service';
import { ApiError } from '../../core/services/api-client.service';
import { environment } from '../../../environments/environment';
import type { DemoAccount } from './demo-accounts';

@Component({
  selector: 'app-login',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './login.component.html',
  styleUrls: ['./login.component.css'],
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

    // Dynamically imported so this UI path (rendered only when demoMode is
    // true — see login.component.html) doesn't force the main bundle to
    // eagerly load it. The actual security boundary against shipping real
    // dev credentials in a production build is NOT this runtime check —
    // esbuild still emits a dynamic import()'s target as a fetchable
    // static chunk even when the branch that triggers it never runs — it
    // is angular.json's `fileReplacements`, which compiles this file's
    // production sibling (an empty DEMO_ACCOUNTS stub) instead of
    // demo-accounts.development.ts for anything but the `development`
    // build configuration. See demo-accounts.ts's own doc comment.
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
        this.router.navigateByUrl(
          redirectTo && redirectTo.startsWith('/app') ? redirectTo : '/app/dashboard',
        );
      },
      error: (err: ApiError) => {
        this.submitting.set(false);
        this.errorMessage.set(err.message);
      },
    });
  }

  goToLanding(event?: Event) {
    if (event) event.preventDefault();
    this.router.navigateByUrl('/landing');
  }
}
