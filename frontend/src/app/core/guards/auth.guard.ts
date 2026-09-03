import { inject } from '@angular/core';
import { CanActivateFn, Router } from '@angular/router';
import { AuthService } from '../services/auth.service';

/**
 * Guards every route under /app (see app.routes.ts). The backend remains
 * the authoritative access-control layer regardless (RBAC/ABAC/RLS —
 * see docs/SECURITY.md); this guard only prevents an unauthenticated
 * browser tab from rendering a protected screen's shell before its first
 * API call would fail with a 401 — a UX guard, not a security boundary
 * unto itself.
 */
export const authGuard: CanActivateFn = (_route, state) => {
  const auth = inject(AuthService);
  const router = inject(Router);

  if (auth.isAuthenticated()) return true;

  return router.createUrlTree(['/login'], { queryParams: { redirectTo: state.url } });
};
