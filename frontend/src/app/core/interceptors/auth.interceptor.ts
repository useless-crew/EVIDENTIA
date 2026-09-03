import { HttpErrorResponse, HttpEvent, HttpHandlerFn, HttpInterceptorFn, HttpRequest } from '@angular/common/http';
import { inject } from '@angular/core';
import { Router } from '@angular/router';
import { Observable, catchError, finalize, shareReplay, switchMap, throwError } from 'rxjs';
import { AuthService } from '../services/auth.service';

// Module-level (not per-request) so every request sharing this interceptor
// instance sees the same in-flight refresh — this is what makes several
// requests failing simultaneously on an expired access token trigger
// exactly ONE POST /auth/refresh (and therefore one refresh-token
// rotation), with the others replayed against its result instead of each
// independently racing to rotate the token (which would revoke each
// other's rotation and fail most of them). shareReplay(1) multicasts the
// single refresh() call's eventual token OR error to every subscriber
// that arrives while it's pending; `finalize` clears the shared reference
// once it settles, so the NEXT distinct 401 starts a fresh attempt rather
// than replaying a stale result forever.
let refreshInFlight$: Observable<string> | null = null;

/** Login/refresh themselves must never be Bearer-authenticated or trigger
 * a refresh-on-401 (a failed login is a credentials problem, not an
 * expired-token problem). */
function isAuthEndpoint(url: string): boolean {
  return url.includes('/auth/login') || url.includes('/auth/refresh');
}

function withAuth(req: HttpRequest<unknown>, token: string): HttpRequest<unknown> {
  return req.clone({ setHeaders: { Authorization: `Bearer ${token}` } });
}

/**
 * Attaches `Authorization: Bearer <access_token>` to every request except
 * login/refresh (see docs/SECURITY.md — the backend never trusts
 * X-User-ID/X-Role, so this interceptor never sends anything but the real
 * bearer token), and on a 401 from any OTHER request, attempts exactly one
 * silent refresh-and-retry (master prompt §11): the failed request is
 * replayed once with the new token; if refresh itself fails, the local
 * session is cleared and the user is sent to /login with a
 * session-expired flag the login screen can surface.
 */
export const authInterceptor: HttpInterceptorFn = (req, next) => {
  const auth = inject(AuthService);
  const router = inject(Router);

  const outgoing = isAuthEndpoint(req.url) ? req : maybeAuthorize(req, auth.accessToken());

  return next(outgoing).pipe(
    catchError((err: unknown) => {
      if (err instanceof HttpErrorResponse && err.status === 401 && !isAuthEndpoint(req.url)) {
        return handleUnauthorized(req, next, auth, router);
      }
      return throwError(() => err);
    })
  );
};

function maybeAuthorize(req: HttpRequest<unknown>, token: string | null): HttpRequest<unknown> {
  return token ? withAuth(req, token) : req;
}

function handleUnauthorized(
  originalReq: HttpRequest<unknown>,
  next: HttpHandlerFn,
  auth: AuthService,
  router: Router
): Observable<HttpEvent<unknown>> {
  if (!refreshInFlight$) {
    refreshInFlight$ = auth.refresh().pipe(
      finalize(() => {
        refreshInFlight$ = null;
      }),
      shareReplay(1)
    );
  }

  return refreshInFlight$.pipe(
    switchMap((newToken) => next(withAuth(originalReq, newToken))),
    catchError((refreshErr) => {
      auth.clearSession();
      router.navigate(['/login'], { queryParams: { sessionExpired: '1' } });
      return throwError(() => refreshErr);
    })
  );
}
