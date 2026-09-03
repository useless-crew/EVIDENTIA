import { Component, EventEmitter, Output, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { AdminUserService } from '../../core/services/admin-user.service';
import { ApiError } from '../../core/services/api-client.service';
import { AdminUser, Role } from '../../core/models/api.models';

/**
 * POST /admin/users, in a small modal — the only way Evidentia creates a
 * user (see docs/API_ENDPOINTS.md's Admin section and the root README's
 * production authentication flow: the initial ADMIN is bootstrapped once
 * server-side, every other account — including every other ADMIN — is
 * created here by an existing ADMIN). id/status defaults/created_at are
 * never sent as client choices beyond the optional initial status field;
 * the backend validates everything server-side regardless of what this
 * form allows through.
 */
@Component({
  selector: 'app-create-user-modal',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './create-user-modal.component.html',
  styleUrls: ['./create-user-modal.component.css'],
})
export class CreateUserModalComponent {
  @Output() closed = new EventEmitter<void>();
  @Output() created = new EventEmitter<AdminUser>();

  private readonly adminUsers = inject(AdminUserService);

  readonly roles: Role[] = ['POLICE', 'FORENSICS', 'LAWYER', 'JUDGE', 'ADMIN'];

  email = '';
  password = '';
  firstName = '';
  lastName = '';
  displayName = '';
  phone = '';
  role: Role = 'POLICE';
  active = true;

  readonly submitting = signal(false);
  readonly errorMessage = signal<string | null>(null);

  close() {
    if (this.submitting()) return;
    this.closed.emit();
  }

  submit() {
    if (this.submitting()) return;
    if (!this.email.trim() || !this.firstName.trim() || !this.lastName.trim()) {
      this.errorMessage.set('Email, first name, and last name are required.');
      return;
    }
    if (this.password.length < 8) {
      this.errorMessage.set('Password must be at least 8 characters.');
      return;
    }

    this.submitting.set(true);
    this.errorMessage.set(null);

    this.adminUsers
      .create({
        email: this.email.trim(),
        password: this.password,
        first_name: this.firstName.trim(),
        last_name: this.lastName.trim(),
        display_name: this.displayName.trim() || undefined,
        phone: this.phone.trim() || undefined,
        role: this.role,
        status: this.active ? 'active' : 'inactive',
      })
      .subscribe({
        next: (user) => {
          this.submitting.set(false);
          this.created.emit(user);
        },
        error: (err: ApiError) => {
          this.submitting.set(false);
          this.errorMessage.set(err.message);
        },
      });
  }
}
