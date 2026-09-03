import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import {
  AdminUser,
  AdminUserListFilter,
  AdminUserListResult,
  CreateUserRequest,
  ResetPasswordRequest,
  RoleCatalogEntry,
  UpdateUserRequest,
  UpdateUserRoleRequest,
  UpdateUserStatusRequest,
} from '../models/api.models';
import { ApiClientService } from './api-client.service';

/**
 * System 8's admin user-management endpoints, exactly as
 * docs/API_ENDPOINTS.md's "Admin" section documents them — every route
 * here is ADMIN-only server-side (RBAC `user:*` — see that doc), and this
 * service does no client-side authorization of its own: a non-admin
 * caller gets the identical 403 ApiError as any other unauthorized
 * request, handled the same way as everywhere else in the app.
 */
@Injectable({ providedIn: 'root' })
export class AdminUserService {
  private readonly api = inject(ApiClientService);

  /** GET /admin/users — filtered, paginated. */
  list(filter: AdminUserListFilter = {}): Observable<AdminUserListResult> {
    return this.api.get<AdminUserListResult>('/admin/users', { ...filter });
  }

  /** GET /admin/users/:id */
  get(id: string): Observable<AdminUser> {
    return this.api.get<AdminUser>(`/admin/users/${id}`);
  }

  /** POST /admin/users — email/password/first_name/last_name/role
   * required; display_name/phone/status optional (status defaults to
   * active server-side). */
  create(request: CreateUserRequest): Observable<AdminUser> {
    return this.api.post<AdminUser>('/admin/users', request);
  }

  /** PUT /admin/users/:id — full replacement of the mutable profile
   * fields (see UpdateUserRequest's own doc). */
  update(id: string, request: UpdateUserRequest): Observable<AdminUser> {
    return this.api.put<AdminUser>(`/admin/users/${id}`, request);
  }

  /** PUT /admin/users/:id/role — replaces the target's entire role set
   * with a single new role. The backend denies an actor changing their
   * OWN role even when they are ADMIN — surfaced as a normal 403
   * ApiError, not a special case this method needs to detect itself. */
  updateRole(id: string, request: UpdateUserRoleRequest): Observable<AdminUser> {
    return this.api.put<AdminUser>(`/admin/users/${id}/role`, request);
  }

  /** PUT /admin/users/:id/status — activate/deactivate/suspend. A
   * non-active status immediately revokes every one of that user's
   * sessions server-side. The backend denies an actor changing their OWN
   * status the same way it denies self-role-change. */
  updateStatus(id: string, request: UpdateUserStatusRequest): Observable<AdminUser> {
    return this.api.put<AdminUser>(`/admin/users/${id}/status`, request);
  }

  /** PUT /admin/users/:id/password — admin-initiated reset; the new
   * password is never echoed back (204 No Content). */
  resetPassword(id: string, request: ResetPasswordRequest): Observable<void> {
    return this.api.put<void>(`/admin/users/${id}/password`, request);
  }

  /** GET /admin/roles — the fixed role catalog. Requires authentication
   * only, not the ADMIN-only gate every other method here has. */
  listRoles(): Observable<RoleCatalogEntry[]> {
    return this.api.get<RoleCatalogEntry[]>('/admin/roles');
  }

  /** GET /users/me — the caller's own profile. Any authenticated role. */
  getOwnProfile(): Observable<AdminUser> {
    return this.api.get<AdminUser>('/users/me');
  }
}
