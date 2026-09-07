import { Component, OnDestroy, OnInit, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { AdminUserService } from '../../core/services/admin-user.service';
import { AdminService } from '../../core/services/admin.service';
import { AuthService } from '../../core/services/auth.service';
import { ApiError } from '../../core/services/api-client.service';
import { EventStreamService } from '../../core/services/event-stream.service';
import {
  AdminBlockchainInfo,
  AdminDashboardStats,
  AdminJobsSummary,
  AdminSystemHealth,
  AdminUser,
  AdminUserListResult,
  Role,
  UserStatus,
} from '../../core/models/api.models';
import { CreateUserModalComponent } from '../../components/create-user-modal/create-user-modal.component';
import { ResetPasswordModalComponent } from '../../components/reset-password-modal/reset-password-modal.component';

export type AdminTab = 'overview' | 'users' | 'system' | 'jobs' | 'blockchain' | 'tools';

const PAGE_SIZE = 20;

/**
 * Administration & Infrastructure Management Hub (System 8 & 21).
 * Consolidates user management, live operational metrics, component health diagnostics,
 * Asynq background queue inspections, Hyperledger Fabric anchor status,
 * and direct management console links for Adminer and MinIO.
 */
@Component({
  selector: 'app-admin',
  standalone: true,
  imports: [CommonModule, FormsModule, CreateUserModalComponent, ResetPasswordModalComponent],
  templateUrl: './admin.component.html',
  styleUrls: ['./admin.component.css'],
})
export class AdminComponent implements OnInit, OnDestroy {
  private readonly adminUsers = inject(AdminUserService);
  private readonly adminService = inject(AdminService);
  private readonly auth = inject(AuthService);
  private readonly eventStream = inject(EventStreamService);
  private stopEventStream: (() => void) | null = null;

  readonly activeTab = signal<AdminTab>('overview');

  // ---- Hub Data Signals ----
  readonly statsLoading = signal(false);
  readonly stats = signal<AdminDashboardStats | null>(null);

  readonly healthLoading = signal(false);
  readonly health = signal<AdminSystemHealth | null>(null);

  readonly jobsLoading = signal(false);
  readonly jobs = signal<AdminJobsSummary | null>(null);

  readonly blockchainLoading = signal(false);
  readonly blockchain = signal<AdminBlockchainInfo | null>(null);

  // ---- Users Tab State ----
  readonly roles: Role[] = ['ADMIN', 'POLICE', 'FORENSICS', 'LAWYER', 'JUDGE'];
  readonly statuses: UserStatus[] = ['active', 'inactive', 'suspended'];
  readonly skeletonRows = Array.from({ length: 6 });

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

  readonly currentUserId = this.auth.currentUser()?.id ?? null;

  ngOnInit() {
    this.fetchOverview();
    this.fetchUsers();
    this.stopEventStream = this.eventStream.connect('/admin/users/events', () => {
      this.fetchUsers();
      this.fetchOverview();
    });
  }

  ngOnDestroy() {
    this.stopEventStream?.();
  }

  setTab(tab: AdminTab) {
    this.activeTab.set(tab);
    if (tab === 'overview') this.fetchOverview();
    if (tab === 'system') this.fetchSystemHealth();
    if (tab === 'jobs') this.fetchJobs();
    if (tab === 'blockchain') this.fetchBlockchain();
    if (tab === 'users') this.fetchUsers();
  }

  fetchOverview() {
    this.statsLoading.set(true);
    this.adminService.getDashboardStats().subscribe({
      next: (res) => {
        this.stats.set(res);
        this.statsLoading.set(false);
      },
      error: () => this.statsLoading.set(false),
    });
    this.fetchSystemHealth();
  }

  fetchSystemHealth() {
    this.healthLoading.set(true);
    this.adminService.getSystemHealth().subscribe({
      next: (res) => {
        this.health.set(res);
        this.healthLoading.set(false);
      },
      error: () => this.healthLoading.set(false),
    });
  }

  fetchJobs() {
    this.jobsLoading.set(true);
    this.adminService.getJobsSummary().subscribe({
      next: (res) => {
        this.jobs.set(res);
        this.jobsLoading.set(false);
      },
      error: () => this.jobsLoading.set(false),
    });
  }

  fetchBlockchain() {
    this.blockchainLoading.set(true);
    this.adminService.getBlockchainInfo().subscribe({
      next: (res) => {
        this.blockchain.set(res);
        this.blockchainLoading.set(false);
      },
      error: () => this.blockchainLoading.set(false),
    });
  }

  fetchUsers() {
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
    this.fetchUsers();
  }

  onRoleFilterChange(value: string) {
    this.roleFilter.set(value as Role | '');
    this.page.set(1);
    this.fetchUsers();
  }

  onStatusFilterChange(value: string) {
    this.statusFilter.set(value as UserStatus | '');
    this.page.set(1);
    this.fetchUsers();
  }

  goToPage(p: number) {
    const meta = this.result()?.meta;
    if (!meta || p < 1 || p > meta.total_pages) return;
    this.page.set(p);
    this.fetchUsers();
  }

  openCreate() {
    this.createOpen.set(true);
  }

  onUserCreated(_user: AdminUser) {
    this.createOpen.set(false);
    this.page.set(1);
    this.fetchUsers();
    this.fetchOverview();
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
        this.fetchUsers();
        this.fetchOverview();
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
        this.fetchUsers();
        this.fetchOverview();
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
