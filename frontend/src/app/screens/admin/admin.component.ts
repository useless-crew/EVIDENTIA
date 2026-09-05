import { Component, OnDestroy, OnInit, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { AdminUserService } from '../../core/services/admin-user.service';
import { AuthService } from '../../core/services/auth.service';
import { ApiError } from '../../core/services/api-client.service';
import { EventStreamService } from '../../core/services/event-stream.service';
import { AdminUser, AdminUserListResult, Role, UserStatus } from '../../core/models/api.models';
import { CreateUserModalComponent } from '../../components/create-user-modal/create-user-modal.component';
import { ResetPasswordModalComponent } from '../../components/reset-password-modal/reset-password-modal.component';

const PAGE_SIZE = 20;

/**
 * Admin -> User Management (System 8). Replaces the old
 * dms.adminUsers mock (illustrative-only, per that array's own doc
 * comment — no user-management endpoint existed through System 7) with
 * real data from AdminUserService, backed entirely by /api/v1/admin/*.
 * The route this renders behind is guarded by adminGuard, but that is a
 * UX convenience only — every action below still goes through the
 * backend's own RBAC checks (see docs/API_ENDPOINTS.md's Admin section),
 * so a non-admin who somehow reaches this screen gets 403 ApiErrors, not
 * a working admin console.
 *
 * System 14 additionally subscribes to GET /admin/users/events (System
 * 13's shared SSE infrastructure) so this list refreshes automatically
 * when a DIFFERENT admin session creates/updates/changes a user — every
 * event is treated identically here as a plain "refetch" signal (see
 * docs/REALTIME_EVENTS.md); this component never renders an event's own
 * payload as if it were current state.
 */
@Component({
  selector: 'app-admin',
  standalone: true,
  imports: [CommonModule, CreateUserModalComponent, ResetPasswordModalComponent],
  templateUrl: './admin.component.html',
  styleUrls: ['./admin.component.css'],
})
export class AdminComponent implements OnInit, OnDestroy {
  private readonly adminUsers = inject(AdminUserService);
  private readonly auth = inject(AuthService);
  private readonly eventStream = inject(EventStreamService);
  private stopEventStream: (() => void) | null = null;

  readonly roles: Role[] = ['ADMIN', 'POLICE', 'FORENSICS', 'LAWYER', 'JUDGE'];
  readonly statuses: UserStatus[] = ['active', 'inactive', 'suspended'];

  readonly loading = signal(true);
  readonly errorMessage = signal<string | null>(null);
  readonly result = signal<AdminUserListResult | null>(null);
  readonly page = signal(1);
  readonly searchTerm = signal('');
  readonly roleFilter = signal<Role | ''>('');
  readonly statusFilter = signal<UserStatus | ''>('');

  readonly createOpen = signal(false);
  readonly resetPasswordFor = signal<AdminUser | null>(null);
  readonly rowActionError = signal<string | null>(null);
  readonly rowBusyId = signal<string | null>(null);

  /** The signed-in caller's own id — an admin cannot change their own
   * role/status through this screen (the backend denies it regardless;
   * this just hides the futile action so nobody has to discover that by
   * trying it). */
  readonly currentUserId = this.auth.currentUser()?.id ?? null;

  ngOnInit() {
    this.fetch();
    this.stopEventStream = this.eventStream.connect('/admin/users/events', () => this.fetch());
  }

  ngOnDestroy() {
    this.stopEventStream?.();
  }

  fetch() {
    this.loading.set(true);
    this.errorMessage.set(null);
    this.adminUsers
      .list({
        page: this.page(),
        page_size: PAGE_SIZE,
        search: this.searchTerm().trim() || undefined,
        role: this.roleFilter() || undefined,
        status: this.statusFilter() || undefined,
      })
      .subscribe({
        next: (res) => {
          this.result.set(res);
          this.loading.set(false);
        },
        error: (err: ApiError) => {
          this.errorMessage.set(err.message);
          this.loading.set(false);
        },
      });
  }

  onSearchInput(value: string) {
    this.searchTerm.set(value);
    this.page.set(1);
    this.fetch();
  }

  onRoleFilterChange(value: string) {
    this.roleFilter.set(value as Role | '');
    this.page.set(1);
    this.fetch();
  }

  onStatusFilterChange(value: string) {
    this.statusFilter.set(value as UserStatus | '');
    this.page.set(1);
    this.fetch();
  }

  goToPage(p: number) {
    const meta = this.result()?.meta;
    if (!meta || p < 1 || p > meta.total_pages) return;
    this.page.set(p);
    this.fetch();
  }

  openCreate() {
    this.createOpen.set(true);
  }

  onUserCreated(_user: AdminUser) {
    this.createOpen.set(false);
    this.page.set(1);
    this.fetch();
  }

  openResetPassword(user: AdminUser) {
    this.resetPasswordFor.set(user);
  }

  onPasswordReset() {
    this.resetPasswordFor.set(null);
  }

  onRoleSelect(user: AdminUser, newRole: string) {
    if (!newRole || newRole === user.roles[0]) return;
    this.rowActionError.set(null);
    this.rowBusyId.set(user.id);
    this.adminUsers.updateRole(user.id, { role: newRole as Role }).subscribe({
      next: () => {
        this.rowBusyId.set(null);
        this.fetch();
      },
      error: (err: ApiError) => {
        this.rowBusyId.set(null);
        this.rowActionError.set(err.message);
      },
    });
  }

  toggleStatus(user: AdminUser) {
    const nextStatus: UserStatus = user.status === 'active' ? 'inactive' : 'active';
    const verb = nextStatus === 'active' ? 'reactivate' : 'deactivate';
    if (!confirm(`${verb === 'deactivate' ? 'Deactivate' : 'Reactivate'} ${user.first_name} ${user.last_name}?`)) return;

    this.rowActionError.set(null);
    this.rowBusyId.set(user.id);
    this.adminUsers.updateStatus(user.id, { status: nextStatus }).subscribe({
      next: () => {
        this.rowBusyId.set(null);
        this.fetch();
      },
      error: (err: ApiError) => {
        this.rowBusyId.set(null);
        this.rowActionError.set(err.message);
      },
    });
  }

  displayName(user: AdminUser): string {
    return user.display_name || `${user.first_name} ${user.last_name}`;
  }

  primaryRole(user: AdminUser): string {
    return user.roles[0] ?? '—';
  }
}
