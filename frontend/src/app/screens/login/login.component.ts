import { Component, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute, Router } from '@angular/router';
import { AuthService } from '../../core/services/auth.service';
import { ApiError } from '../../core/services/api-client.service';

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

  email = '';
  password = '';

  readonly submitting = signal(false);
  readonly errorMessage = signal<string | null>(null);
  readonly sessionExpired = signal(false);

  constructor() {
    this.sessionExpired.set(this.route.snapshot.queryParamMap.get('sessionExpired') === '1');
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
