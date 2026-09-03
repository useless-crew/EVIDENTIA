# Evidentia Frontend

Angular 22 application (standalone components, no NgModules) for
Evidentia — connects to the real Go/Gin backend under `../backend/`; see
the root [README.md](../README.md) and [ARCHITECTURE.md](../ARCHITECTURE.md)
for the full-stack picture.

## Backend Integration

`src/app/core/` holds the entire frontend↔backend integration layer:

- `services/api-client.service.ts` — the one centralized HTTP client
  (base URL, `pkg/response` envelope unwrap, one consistent `ApiError`
  shape). No other file calls `HttpClient` directly for a backend
  request.
- `services/auth.service.ts` + `interceptors/auth.interceptor.ts` — real
  `POST /auth/{login,refresh,logout}`, automatic single-flight
  refresh-and-retry-once on a `401`, session persisted via
  `services/token-storage.service.ts`.
- `guards/auth.guard.ts` — protects every `/app/**` route.
- `services/case.service.ts` / `services/document.service.ts` — System
  5/6/7 endpoints (cases, upload/download, `POST /documents/:id/verify`,
  `GET /documents/:id/certificate`).
- `models/api.models.ts` — TypeScript types matching the backend's actual
  JSON field names (snake_case) exactly.

`docs/API_ENDPOINTS.md` (repository root) is the authoritative contract —
these services implement exactly what it (and the live backend) document,
nothing invented.

## Configuration

Angular's CLI (esbuild-based `@angular/build:application`) does not read
`.env` files — the project's real, functioning equivalent of a Vite
`VITE_API_BASE_URL` is `src/environments/environment*.ts` +
`angular.json`'s `fileReplacements`:

- `src/environments/environment.development.ts` — used by `ng serve`
  and `ng build --configuration development`. Defaults to
  `http://localhost:8080/api/v1` (the backend's default port — see the
  root `docker-compose.yml`/`.env.example`).
- `src/environments/environment.ts` — used by a plain `ng build`
  (production). Defaults to `/api/v1` (same-origin, for a deployment that
  reverse-proxies the API alongside the built static files). Override
  this file's value at build time for a deployment where the backend is
  served from a different origin — never hardcode a specific production
  domain into source control.

## Development server

Start the backend first (see the root README's "Full stack via Docker
Compose" or "Backend directly on the host"), then:

```bash
npm install
npm start        # ng serve — http://localhost:4200
```

The dev server's default port (4200) matches the backend's default CORS
allowlist (`CORS_ALLOWED_ORIGINS=http://localhost:4200` — see
`../.env.example`), so no CORS configuration is needed for local
development.

### Getting your first login-able accounts

A fresh database has no login-able accounts — production real user
management (System 8) is admin-driven: set
`EVIDENTIA_BOOTSTRAP_ADMIN_EMAIL`/`_PASSWORD`/`_NAME` before the backend's
first startup (see `../.env.example`) to provision the one initial ADMIN
account, sign in as that ADMIN, then use Admin → Users in the app (or
`POST /api/v1/admin/users`) to create POLICE/FORENSICS/LAWYER/JUDGE/ADMIN
accounts for everyone else — see `../docs/API_ENDPOINTS.md`'s Admin
section.

For local development without going through that flow every time,
`backend/cmd/devuser` remains a quicker shortcut (see that command's own
doc comment for why it exists and why it's safe — nothing it does is
wired into any HTTP route):

```bash
cd ../backend
export DATABASE_MIGRATOR_USER=evidentia DATABASE_MIGRATOR_PASSWORD=changeme_example \
       DATABASE_HOST=localhost DATABASE_NAME=evidentia
go run ./cmd/devuser -email=police@example.test -password=at-least-8-chars -first=Jane -last=Doe -role=POLICE
```

Repeat with `-role=ADMIN`/`FORENSICS`/`LAWYER`/`JUDGE` as needed. These
are local-development-only accounts you create yourself with a password
you choose — nothing here is a real credential, and none is committed
anywhere. When `environment.development.ts`'s `demoMode` is true (the
default for `ng serve`), the login screen's "Local Dev Demo Accounts"
chips are a convenience for prefilling the sign-in form with accounts
created this way — they still submit through the real `POST /auth/login`
(see `src/app/screens/login/login.component.ts`'s own comment). That
panel is compiled out of a production build (`demoMode: false` in
`environment.ts`) — production login is just an email/password form.

## Code scaffolding

```bash
ng generate component component-name
ng generate --help   # full schematic list
```

## Building

```bash
ng build                               # production (dist/evidentia)
ng build --configuration development
```

## Running unit tests

```bash
ng test    # Vitest
```

## What's real vs. still illustrative

Connected to the real backend: authentication, cases (list/detail/
create), document upload/download, and System 7's verify/certificate
flows. Dashboard aggregate stats, the audit-log table/chain-graph,
redaction, admin user management, and the access-policy preview remain
illustrative mock content — no backend endpoint exists yet for any of
them (audit read, redaction, user administration, and audit-chain
verification are later systems' scope; see `../ARCHITECTURE.md`). Each is
commented in `src/app/core/services/dms-state.service.ts` at the exact
point it's still mock, so it's never ambiguous which is which.

## Additional Resources

[Angular CLI Overview and Command Reference](https://angular.dev/tools/cli).
