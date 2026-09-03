import { Injectable } from '@angular/core';
import { AuthUser } from '../models/api.models';

/** What's persisted across a page reload — never anything beyond this. */
export interface StoredSession {
  accessToken: string;
  refreshToken: string;
  user: AuthUser;
}

const STORAGE_KEY = 'evidentia_session';

/**
 * Encapsulates the ONE piece of frontend-persisted auth state:
 * access/refresh tokens + the user object the backend returned them for.
 * localStorage is used because Evidentia's backend issues tokens as plain
 * JSON body values (not cookies — see docs/API_ENDPOINTS.md), so the
 * client, not the browser, is responsible for carrying them across a
 * reload; this mirrors the storage key/shape the previous (mock) session
 * code already used, so restoring an existing session on reload keeps
 * working unchanged.
 *
 * This is the ONLY place that touches localStorage for auth — AuthService
 * and the auth interceptor read/write through here, never directly.
 */
@Injectable({ providedIn: 'root' })
export class TokenStorageService {
  load(): StoredSession | null {
    try {
      if (typeof window === 'undefined' || !window.localStorage) return null;
      const raw = localStorage.getItem(STORAGE_KEY);
      if (!raw) return null;
      const parsed = JSON.parse(raw);
      if (parsed && parsed.accessToken && parsed.refreshToken && parsed.user) {
        return parsed as StoredSession;
      }
      return null;
    } catch {
      return null;
    }
  }

  save(session: StoredSession): void {
    try {
      if (typeof window === 'undefined' || !window.localStorage) return;
      localStorage.setItem(STORAGE_KEY, JSON.stringify(session));
    } catch {
      // Storage unavailable (private browsing, quota) — the session
      // simply won't survive a reload; not a reason to fail the request
      // that got us here.
    }
  }

  clear(): void {
    try {
      if (typeof window === 'undefined' || !window.localStorage) return;
      localStorage.removeItem(STORAGE_KEY);
    } catch {
      // Nothing more we can do.
    }
  }
}
