import { inject } from '@angular/core';
import { CanActivateFn, Router } from '@angular/router';
import { AuthService } from '../services/auth.service';

/**
 * Guards /app/admin (see app.routes.ts). Same UX-only role as authGuard —
 * the backend independently enforces this (every /admin/* route requires
 * the ADMIN-only user:* RBAC permissions; see
 * internal/httpserver/router.go and docs/API_ENDPOINTS.md's Admin
 * section), so a manipulated/bypassed frontend route gains nothing: the
 * API call itself still gets a 403. This guard only prevents a non-admin
 * browser tab from rendering the admin shell before that first API call
 * would fail.
 */
export const adminGuard: CanActivateFn = () => {
  const auth = inject(AuthService);
  const router = inject(Router);

  if (auth.role() === 'ADMIN') return true;

  return router.createUrlTree(['/app/dashboard']);
};
