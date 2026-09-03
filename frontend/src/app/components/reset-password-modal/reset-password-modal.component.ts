import { Component, EventEmitter, Input, Output, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { AdminUserService } from '../../core/services/admin-user.service';
import { ApiError } from '../../core/services/api-client.service';
import { AdminUser } from '../../core/models/api.models';

/**
 * PUT /admin/users/:id/password, in a small modal — this project's
 * admin-driven password-reset mechanism (no email/token flow — see
 * UserService.ResetPassword's own doc comment). The new password is
 * never echoed back by the backend (204 No Content) and is never logged
 * here either.
 */
@Component({
  selector: 'app-reset-password-modal',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './reset-password-modal.component.html',
  styleUrls: ['./reset-password-modal.component.css'],
})
export class ResetPasswordModalComponent {
  @Input({ required: true }) user!: AdminUser;
  @Output() closed = new EventEmitter<void>();
  @Output() done = new EventEmitter<void>();

  private readonly adminUsers = inject(AdminUserService);

  password = '';

  readonly submitting = signal(false);
  readonly errorMessage = signal<string | null>(null);

  close() {
    if (this.submitting()) return;
    this.closed.emit();
  }

  submit() {
    if (this.submitting()) return;
    if (this.password.length < 8) {
      this.errorMessage.set('Password must be at least 8 characters.');
      return;
    }

    this.submitting.set(true);
    this.errorMessage.set(null);

    this.adminUsers.resetPassword(this.user.id, { password: this.password }).subscribe({
      next: () => {
        this.submitting.set(false);
        this.done.emit();
      },
      error: (err: ApiError) => {
        this.submitting.set(false);
        this.errorMessage.set(err.message);
      },
    });
  }
}
