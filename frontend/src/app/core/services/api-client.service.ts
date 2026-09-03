import { HttpClient, HttpErrorResponse, HttpEventType, HttpResponse } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable, catchError, defer, filter, from, map, switchMap, throwError } from 'rxjs';
import { environment } from '../../../environments/environment';
import { ApiEnvelope } from '../models/api.models';

/**
 * Normalized shape every failed API call surfaces as — whether the
 * backend responded with its own error envelope (pkg/response.ErrorBody)
 * or the request never reached it at all (network failure, CORS,
 * timeout). `message` is always safe to show a user directly; it is
 * never raw HTTP/driver/stack-trace text (master prompt §28).
 */
export class ApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string,
    public readonly requestId?: string
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

/** One event of an in-flight multipart upload — see postMultipart. */
export type UploadEvent<T> = { type: 'progress'; percent: number } | { type: 'done'; data: T };

/**
 * The single centralized HTTP client every Evidentia API call goes
 * through — base URL, the pkg/response envelope unwrap, and one
 * consistent error shape (ApiError). No component or other service talks
 * to HttpClient directly for a backend call.
 *
 * Authorization headers are attached by AuthInterceptor
 * (../interceptors/auth.interceptor.ts), not here — this class only knows
 * how to shape requests/responses, not about auth state.
 */
@Injectable({ providedIn: 'root' })
export class ApiClientService {
  private readonly http = inject(HttpClient);
  private readonly base = environment.apiBaseUrl;

  get<T>(path: string, query?: Record<string, string | number | boolean | undefined>): Observable<T> {
    return this.http.get<ApiEnvelope<T>>(this.url(path), { params: this.buildParams(query) }).pipe(
      map((env) => this.unwrap(env)),
      catchError((err) => this.rethrow(err))
    );
  }

  post<T>(path: string, body?: unknown): Observable<T> {
    return this.http.post<ApiEnvelope<T>>(this.url(path), body ?? {}).pipe(
      map((env) => this.unwrap(env)),
      catchError((err) => this.rethrow(err))
    );
  }

  put<T>(path: string, body: unknown): Observable<T> {
    return this.http.put<ApiEnvelope<T>>(this.url(path), body).pipe(
      map((env) => this.unwrap(env)),
      catchError((err) => this.rethrow(err))
    );
  }

  /**
   * A multipart POST (document upload) with real upload-progress events —
   * see internal/handlers/document/upload.go: the file is streamed
   * server-side, so this reports the CLIENT->SERVER transfer progress,
   * the only progress a browser can observe.
   */
  postMultipart<T>(path: string, form: FormData): Observable<UploadEvent<T>> {
    return this.http
      .post<ApiEnvelope<T>>(this.url(path), form, { reportProgress: true, observe: 'events' })
      .pipe(
        map((event): UploadEvent<T> | null => {
          if (event.type === HttpEventType.UploadProgress) {
            const percent = event.total ? Math.round((100 * event.loaded) / event.total) : 0;
            return { type: 'progress', percent };
          }
          if (event.type === HttpEventType.Response) {
            return { type: 'done', data: this.unwrap(event.body as ApiEnvelope<T>) };
          }
          return null; // Sent/ResponseHeader/etc. — not interesting to callers.
        }),
        filter((event): event is UploadEvent<T> => event !== null),
        catchError((err) => this.rethrow(err))
      );
  }

  /**
   * A binary GET (document download) — returns the full HttpResponse so
   * the caller can read both the Blob body and the Content-Disposition
   * header (exposed cross-origin by the backend's CORS middleware
   * specifically for this — see internal/middleware/cors_middleware.go).
   */
  getBlob(path: string): Observable<HttpResponse<Blob>> {
    return this.http
      .get(this.url(path), { observe: 'response', responseType: 'blob' })
      .pipe(catchError((err) => this.rethrow(err)));
  }

  private url(path: string): string {
    return `${this.base}${path.startsWith('/') ? path : '/' + path}`;
  }

  private buildParams(query?: Record<string, string | number | boolean | undefined>) {
    const params: Record<string, string> = {};
    if (query) {
      for (const [k, v] of Object.entries(query)) {
        if (v !== undefined && v !== null && v !== '') params[k] = String(v);
      }
    }
    return params;
  }

  private unwrap<T>(env: ApiEnvelope<T>): T {
    if (env && env.success) {
      return env.data as T;
    }
    const err = env?.error;
    throw new ApiError(200, err?.code ?? 'UNKNOWN_ERROR', err?.message ?? 'The server returned an unexpected response.', err?.request_id);
  }

  /**
   * Converts a failed HTTP call into one consistent ApiError, never
   * leaking raw driver/network text. Handles three shapes: a normal JSON
   * error envelope, a Blob-typed error body (getBlob requests get their
   * error body as a Blob too, since responseType governs parsing
   * regardless of status — read and JSON-parse it before giving up), and
   * a request that never reached the backend at all (status 0).
   */
  private rethrow(err: unknown): Observable<never> {
    if (err instanceof ApiError) return throwError(() => err);

    if (err instanceof HttpErrorResponse) {
      if (err.error instanceof Blob) {
        const blob = err.error;
        return defer(() => from(blob.text())).pipe(
          switchMap((text) => {
            const parsed = this.tryParseEnvelope(text);
            return throwError(() => (parsed?.error ? this.errorFromEnvelope(err.status, parsed) : this.genericError(err)));
          })
        );
      }

      const parsed = this.asEnvelope(err.error);
      if (parsed?.error) {
        return throwError(() => this.errorFromEnvelope(err.status, parsed));
      }
      return throwError(() => this.genericError(err));
    }

    return throwError(() => new ApiError(0, 'UNKNOWN_ERROR', 'An unexpected error occurred. Please try again.'));
  }

  private asEnvelope(body: unknown): ApiEnvelope<unknown> | null {
    if (body && typeof body === 'object' && 'success' in body) return body as ApiEnvelope<unknown>;
    return null;
  }

  private tryParseEnvelope(text: string): ApiEnvelope<unknown> | null {
    try {
      return this.asEnvelope(JSON.parse(text));
    } catch {
      return null;
    }
  }

  private errorFromEnvelope(status: number, env: ApiEnvelope<unknown>): ApiError {
    return new ApiError(status, env.error!.code, env.error!.message, env.error!.request_id);
  }

  private genericError(err: HttpErrorResponse): ApiError {
    if (err.status === 0) {
      return new ApiError(0, 'NETWORK_ERROR', 'Unable to reach the server. Check your connection and try again.');
    }
    return new ApiError(err.status, 'UNKNOWN_ERROR', 'An unexpected error occurred. Please try again.');
  }
}
