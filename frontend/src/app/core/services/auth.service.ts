import { Injectable, computed, inject, signal } from '@angular/core';
import { Observable, catchError, map, of, tap, throwError } from 'rxjs';
import { AuthTokens, AuthUser, Role } from '../models/api.models';
import { ApiClientService, ApiError } from './api-client.service';
import { TokenStorageService } from './token-storage.service';

/**
 * Real authentication against Evidentia's backend (System 3):
 * POST /auth/login, POST /auth/refresh, POST /auth/logout. This is the
 * ONLY source of truth for "who is signed in" — no component or other
 * service fabricates a session, and nothing here ever accepts a
 * client-chosen role: `role`/`currentUser` are always exactly what the
 * backend's token response said (see docs/SECURITY.md — the backend
 * re-resolves role from the database on every request regardless of what
 * a client claims).
 *
 * Access token: held in memory (a signal) for the lifetime of the app,
 * and persisted (with the refresh token and user) via TokenStorageService
 * so a page reload doesn't force a fresh login — restored eagerly in the
 * constructor. The access token is short-lived (900s); AuthInterceptor
 * calls refresh() on a 401, not this service's callers directly.
 */
@Injectable({ providedIn: 'root' })
export class AuthService {
  private readonly api = inject(ApiClientService);
  private readonly storage = inject(TokenStorageService);

  private readonly _accessToken = signal<string | null>(null);
  private readonly _user = signal<AuthUser | null>(null);
  private refreshToken: string | null = null;

  readonly accessToken = this._accessToken.asReadonly();
  readonly currentUser = this._user.asReadonly();
  readonly isAuthenticated = computed(() => this._accessToken() !== null);
  readonly role = computed<Role | null>(() => this._user()?.role ?? null);

  constructor() {
    const saved = this.storage.load();
    if (saved) {
      this._accessToken.set(saved.accessToken);
      this._user.set(saved.user);
      this.refreshToken = saved.refreshToken;
    }
  }

  /** POST /auth/login. Rejects with an ApiError (401, generic message —
   * see AuthTokens's own doc: identical for unknown email, wrong
   * password, or an inactive/suspended account) on failure. */
  login(email: string, password: string): Observable<AuthUser> {
    return this.api
      .post<AuthTokens>('/auth/login', { email, password })
      .pipe(
        tap((tokens) => this.applyTokens(tokens)),
        map((tokens) => tokens.user)
      );
  }

  /** POST /auth/refresh, rotating the refresh token (System 3: the
   * presented token is revoked and replaced — see docs/SECURITY.md). Used
   * by AuthInterceptor on a 401; not normally called directly by UI code. */
  refresh(): Observable<string> {
    if (!this.refreshToken) {
      return throwError(() => new ApiError(401, 'UNAUTHORIZED', 'Your session has expired. Please sign in again.'));
    }
    return this.api
      .post<AuthTokens>('/auth/refresh', { refresh_token: this.refreshToken })
      .pipe(
        tap((tokens) => this.applyTokens(tokens)),
        map((tokens) => tokens.access_token)
      );
  }

  /** POST /auth/logout (best-effort — a failed backend call never blocks
   * clearing the local session, since there is nothing else the client
   * can do about an already-broken connection to the server it's trying
   * to sign out of). */
  logout(): Observable<void> {
    const rt = this.refreshToken;
    const call$ = rt ? this.api.post<null>('/auth/logout', { refresh_token: rt }) : of(null);
    return call$.pipe(
      catchError(() => of(null)),
      tap(() => this.clearSession()),
      map(() => undefined)
    );
  }

  /** Clears local session state without calling the backend — used by
   * AuthInterceptor when a refresh itself fails (the refresh token is
   * already invalid, so there is nothing left to revoke). */
  clearSession(): void {
    this._accessToken.set(null);
    this._user.set(null);
    this.refreshToken = null;
    this.storage.clear();
  }

  private applyTokens(tokens: AuthTokens): void {
    this._accessToken.set(tokens.access_token);
    this._user.set(tokens.user);
    this.refreshToken = tokens.refresh_token;
    this.storage.save({
      accessToken: tokens.access_token,
      refreshToken: tokens.refresh_token,
      user: tokens.user,
    });
  }
}
