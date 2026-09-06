# Deployment

## Purpose

How to configure, run, and operate Evidentia — from a bare local
checkout through a production-like Docker Compose deployment with TLS,
a reverse proxy, and a separately-deployed background worker (System
17). This document distinguishes three environments throughout:

- **Development** — `go run ./cmd/server` directly on the host, or the
  base `docker-compose.yml` (backend + postgres + redis + minio, no
  reverse proxy, plain HTTP, loopback-bound infrastructure ports).
- **Demo / production-like** — `docker-compose.yml` +
  `docker-compose.prod.yml` together: the full topology (reverse proxy,
  built frontend, separate worker, TLS) but typically run with a
  self-signed certificate on a single machine for a demonstration.
- **Real production** — the same production-like topology, but with a
  real domain, a real CA/ACME certificate, real secrets from a real
  secret-management mechanism, and infrastructure sized/monitored for
  actual traffic. This document tells you exactly which pieces
  production-like stops short of and what a real deployment must still
  add — see "What this is NOT" near the end.

## Prerequisites

- Docker and Docker Compose v2 (`docker compose version`).
- Go 1.25+ and Node 22+ if building/running outside Docker.
- `openssl` (for generating a local demo TLS certificate).

## 1. Prerequisites & Repository Layout

```
backend/                 Go module (API, worker, migrator — see below)
frontend/                Angular application
docker-compose.yml       Base services: postgres, redis, minio, backend,
                          frontend (profile-gated placeholder preview)
docker-compose.override.yml
                          Auto-loaded local-dev convenience: publishes
                          host ports for the base file's services
docker-compose.prod.yml  Production-like overlay: reverse-proxy, real
                          frontend build, separate worker, migrate
                          init service, resource limits, no host ports
                          on anything but the reverse proxy
ops/reverse-proxy/       nginx config template + self-signed dev-cert
                          generator
backend/certs/           TLS certificate/key the reverse proxy mounts
                          (gitignored — see backend/certs/README.md)
scripts/backup.sh        PostgreSQL + MinIO backup
scripts/restore.sh       Restore from a scripts/backup.sh output dir
```

## 2. Environment Configuration

Three files, three audiences:

- [`../.env.example`](../.env.example) — variables `docker-compose.yml`
  and `docker-compose.prod.yml` substitute into the containers they
  manage (credentials, ports, resource-adjacent knobs).
- [`../backend/.env.example`](../backend/.env.example) — every variable
  `internal/config` reads when running the Go binary directly. Values
  with no default (`DATABASE_USER`, `DATABASE_PASSWORD`,
  `DATABASE_NAME`, `MINIO_ACCESS_KEY`, `MINIO_SECRET_KEY`,
  `MINIO_BUCKET`, `JWT_SIGNING_KEY`) must be set explicitly — the
  process refuses to start otherwise (fail closed).
- `frontend/src/environments/environment.ts` /
  `environment.development.ts` — the Angular-idiomatic equivalent of a
  `.env` file (Angular's esbuild-based CLI does not read `.env` files).
  `apiBaseUrl` is `/api/v1` (relative, same-origin) in the production
  build — it expects the reverse proxy to serve both the frontend and
  `/api/v1/*` from the same origin, exactly as `docker-compose.prod.yml`
  does. **Never put a backend secret in this file** — anything here
  ships to every browser that loads the page.

Copy `.env.example` to `.env` at the repository root to override any
placeholder for either Compose file (`docker compose` reads `.env`
automatically). **Never commit `.env`** — it is gitignored.

## 3. Secret Handling

Never commit: passwords, `JWT_SIGNING_KEY`, MinIO keys,
`CERTIFICATE_SIGNING_KEY`, TLS private keys, or bootstrap-admin
credentials. Concretely:

- `.env` / `.env.*` (except `.env.example`) are gitignored.
- `*.pem`, `*.key`, `*.crt` are gitignored (`backend/certs/` — see that
  directory's own README).
- `/backups/` is gitignored (`scripts/backup.sh`'s default output
  location contains case/document metadata and evidence objects).
- A repository-wide scan for accidental secrets (`grep` for
  `password=`, `secret=`, `-----BEGIN`, known credential variable names
  assigned a non-placeholder value) was performed as part of System 15's
  hardening pass and again for System 17 — see that system's report for
  what was found (nothing beyond the test fixtures already reviewed and
  judged synthetic). **If you ever do find a real secret committed to
  Git history**, removing it from the working tree is not sufficient —
  it remains in every clone's history until the affected commits are
  rewritten (`git filter-repo`/BFG) AND the secret itself is rotated.
  This document does not perform history rewrites automatically; that
  is a deliberate, disruptive operation requiring explicit sign-off from
  whoever owns the repository.

## 4. Development Deployment

```bash
docker compose up -d postgres redis minio
cd backend
DATABASE_MIGRATOR_USER=evidentia DATABASE_MIGRATOR_PASSWORD=changeme_example \
  DATABASE_HOST=localhost DATABASE_NAME=evidentia go run ./cmd/migrate up
go run ./cmd/migrate seed   # roles/permissions — see step 6 below
cd ..
docker compose up -d
```

`docker compose` here auto-loads `docker-compose.yml` +
`docker-compose.override.yml` — no `-f` flags needed, and every
infrastructure port (`localhost:5432`, `:6379`, `:9000`/`:9001`,
`:8080`) is published exactly as before System 17. To use real
credentials for the containers instead of the built-in placeholders:

```bash
cp .env.example .env    # edit POSTGRES_PASSWORD / MINIO_ROOT_PASSWORD / etc.
docker compose up -d
```

Verify:

```bash
curl http://localhost:8080/health
curl http://localhost:8080/ready
docker compose logs -f backend
```

The frontend is not started by default (gated behind the `frontend`
Compose profile) — see [`../frontend/README.md`](../frontend/README.md)
and section 8 below for the full production build.

### Backend on the host (no Docker)

```bash
cd backend
cp .env.example .env
# point DATABASE_*/REDIS_ADDR/MINIO_* at the docker-compose infra above,
# and set DATABASE_MIGRATOR_USER/PASSWORD, JWT_SIGNING_KEY
go mod download
go run ./cmd/migrate up
go run ./cmd/migrate seed
go run ./cmd/server
```

Or via `make` (from `backend/`, or the repo root — the root `Makefile`
delegates to `backend/Makefile`): `make run`, `make build`, `make test`,
`make test-race`, `make vet`, `make fmt`, `make migrate-up` /
`migrate-down`, `make seed`, `make sqlc`, `make swagger`, `make
docker-up` / `docker-down`, `make clean`. `make lint` requires
[golangci-lint](https://golangci-lint.run/welcome/install/).

## 5. Production-like Deployment

```bash
# One-time: a self-signed cert for local/demo HTTPS (see section 9)
./ops/reverse-proxy/generate-dev-certs.sh

export COMPOSE_PROFILES=frontend   # activates the base file's
                                    # profile-gated `frontend` service —
                                    # see docker-compose.prod.yml's own
                                    # comment on that service for why an
                                    # override file cannot do this
docker compose -f docker-compose.yml -f docker-compose.prod.yml build
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

This brings up, in dependency order: `postgres`, `redis`, `minio` →
`migrate` (runs schema migrations + the reference-data seed, then exits
— see section 6) → `backend` (API, embedded worker disabled) + `worker`
(the separate Asynq worker) → `frontend` (built Angular SPA behind
Nginx) → `reverse-proxy` (TLS termination, routing, the only published
ports).

Verify:

```bash
curl http://localhost/healthz            # plain-HTTP liveness, no TLS needed
curl -k https://localhost/health          # -k: the self-signed dev cert
curl -k https://localhost/ready
open https://localhost/                   # browser will warn on the
                                            # self-signed cert — expected
```

`docker compose ... ps` should show every service `healthy` except
`migrate` (one-shot, `Exited (0)` is correct) and `worker` (no HTTP
endpoint to health-check — see `docker-compose.prod.yml`'s comment on
why its inherited image healthcheck is explicitly disabled there).

### What's different from development

| | Development (`docker-compose.yml` + `.override.yml`) | Production-like (`docker-compose.yml` + `.prod.yml`) |
|---|---|---|
| Public entry point | `backend` directly on `:8080` (HTTP) | `reverse-proxy` on `:80`/`:443` (TLS) only |
| postgres/redis/minio | Published to `127.0.0.1` | Not published at all |
| Frontend | Not started by default | Built and served behind Nginx |
| Background worker | Embedded in `backend`'s own process | Separate `worker` container |
| Migrations | Run manually (`go run ./cmd/migrate up`) | `migrate` init service, runs automatically before `backend`/`worker` start |
| `APP_ENV` | `development` | `production` (stricter CORS/wildcard validation — see `internal/config/validate.go`) |

## 6. Database Migrations & Seeding

`backend/db/migrations/`, versioned via `golang-migrate` (used as a Go
library, `cmd/migrate` — not a separately installed CLI). Five
migrations as of System 17: `000001_init_schema` (System 2 — the full
domain schema, RLS, the `evidentia_app` role), `000002_auth_sessions`
(System 3), `000003_certificate_integrity` (System 7),
`000004_document_sharing` (System 9), `000005_audit_verifications`
(System 11). Check `backend/db/migrations/` for the current list — this
document does not repeat it as new systems land, to avoid it going
stale.

```bash
cd backend
DATABASE_MIGRATOR_USER=... DATABASE_MIGRATOR_PASSWORD=... go run ./cmd/migrate up
go run ./cmd/migrate version   # prints version=N dirty=false
go run ./cmd/migrate seed      # roles/permissions/role_permissions —
                                 # idempotent, ON CONFLICT DO NOTHING
```

`seed` applies `backend/db/seed/*.sql` over the same connection `up`
uses — the fixed ADMIN/POLICE/FORENSICS/LAWYER/JUDGE role catalog and
its permission mapping. **This must run before the first login attempt
ever succeeds**: `internal/bootstrap.EnsureBootstrapAdmin` cannot create
the initial ADMIN account without the `ADMIN` role already existing in
the database (confirmed the hard way while validating
`docker-compose.prod.yml` — the fix is exactly this ordering).

In `docker-compose.prod.yml`, the `migrate` service runs `migrate up &&
migrate seed` automatically before `backend`/`worker` start
(`depends_on: migrate: condition: service_completed_successfully`) —
migrations are never run by hand against that deployment. It is a
one-shot service (`restart: "no"`): a failure surfaces as a non-zero
exit code in `docker compose logs migrate`, and `backend`/`worker` never
start, rather than the failure being silently retried forever.

`backend/scripts/seed_db.sh` (`make seed`) is the equivalent host-based
script for the development-on-the-host flow above — both it and `cmd/
migrate seed` read the exact same `backend/db/seed/*.sql` files, so
there is no risk of the two drifting apart.

## 7. Worker Deployment

Two supported topologies, both running the identical `internal/jobs`
code (never a second implementation):

- **Combined** (`docker-compose.yml`'s `backend` service, and `go run
  ./cmd/server` on the host): one process runs the HTTP API and the
  Asynq worker together — see `cmd/server/main.go`'s doc comment for
  why this is the right default for this workload (a handful of
  sequential batched reads per audit-chain verification, not a
  high-throughput queue).
- **Separate** (`docker-compose.prod.yml`): `backend` runs with
  `DISABLE_EMBEDDED_WORKER=true`, and a dedicated `worker` service
  (`cmd/worker`, the exact same `jobs.NewServer`/`NewMux` construction,
  just without also starting an HTTP listener) processes jobs
  independently — restartable, scalable, and failure-isolated from the
  API. This is what a real production deployment should use: an API
  restart no longer interrupts an in-flight audit-chain verification,
  and vice versa.

`cmd/worker` never runs `internal/bootstrap.EnsureBootstrapAdmin` — only
`cmd/server` does, so exactly one process ever attempts the bootstrap
regardless of how many worker replicas you run.

## 8. Frontend Deployment

`frontend/Dockerfile` (System 17): `node:22-alpine` builds the
production Angular bundle (`npm ci && npm run build`), then
`nginxinc/nginx-unprivileged:1.27-alpine` serves only the static output
— no Node/npm/Angular CLI in the runtime image, non-root.

```bash
cd frontend
npm ci                                  # exact, reproducible install
npm run build                           # production bundle (angular.json's
                                          # defaultConfiguration)
docker build -t evidentia-frontend .    # or: docker compose ... build frontend
```

The build output's API base URL (`/api/v1`, relative) is fixed at build
time by `environment.ts` — see section 2. `demoMode` is `false` in that
same file, so the production bundle never renders the local-dev
demo-account quick-sign-in panel, and demo credentials are not present
in the compiled output (verified: `grep`-ing the built `dist/` for the
development environment's demo account strings finds nothing).

## 9. Reverse Proxy & TLS/HTTPS

`ops/reverse-proxy/nginx.conf.template` (Nginx, templated via the
official image's `envsubst`-on-startup mechanism — see that file's own
doc comment) is the only service in `docker-compose.prod.yml` that
publishes a host port. It:

- Redirects all plain HTTP to HTTPS, except `/healthz` (a static, no-
  dependency liveness check usable without a certificate at all).
- Terminates TLS (`TLSv1.2`/`TLSv1.3` only, a modern cipher list),
  reading `backend/certs/fullchain.pem`/`privkey.pem`.
- Routes `/api/*`, `/health`, `/ready` to the `backend` service and
  everything else to the `frontend` service.
- Disables response buffering specifically for the three SSE endpoints
  (`/api/v1/cases/*/events`, `/api/v1/admin/users/events`,
  `/api/v1/audit/verify-chain/*/events`) with a 2-hour read/send
  timeout, so real-time audit-verification/case-notification progress
  is never delayed or cut off by the proxy — see section 10.
- Passes the backend's own security/cache headers through untouched on
  API responses (never overriding `Cache-Control: no-store` with a
  weaker, frontend-oriented value — a real regression caught while
  validating this file, see the template's own doc comment for the
  full explanation) and sets its own full header set (HSTS, CSP-adjacent
  headers) only on the HTML-serving location.

**Local / demo HTTPS**: `./ops/reverse-proxy/generate-dev-certs.sh`
writes a self-signed certificate to `backend/certs/`. Browsers will show
a trust warning — expected and correct, not a bug to work around.

**Real production HTTPS**: replace `backend/certs/fullchain.pem`/
`privkey.pem` with a certificate from your CA or ACME provider (e.g.
Let's Encrypt via `certbot`) for your actual domain, then restart the
`reverse-proxy` service. Automated ACME issuance (a `certbot` sidecar
container, or switching the reverse proxy to Caddy for its built-in
automatic HTTPS) is a reasonable extension for a deployment with a real,
publicly-resolvable domain — deliberately not implemented/tested here,
since this repository has no such domain to validate it against; do not
assume it works without testing it against your real domain.

**Permissions**: the reverse-proxy container reads the certificate as a
non-root user via a read-only bind mount — `privkey.pem` must be
readable by that user (the dev-cert script uses `644`; see
`backend/certs/README.md` for what a real key needs).

## 10. SSE Behind the Reverse Proxy

Validated end-to-end against `docker-compose.prod.yml` (System 17): a
real `POST /audit/verify-chain` → `GET .../events` SSE connection
through HTTPS delivered `AUDIT_VERIFICATION_PROGRESS` →
`AUDIT_VERIFICATION_STARTED` → `AUDIT_VERIFICATION_COMPLETED` frames
with no buffering delay, while the audit-chain job was processed by the
**separate** `worker` container (confirmed via that container's own
logs, not the disabled embedded one). See section 9 for the specific
Nginx directives this requires (`proxy_buffering off`, no chunked
re-encoding, extended timeouts) — removing any of them will silently
reintroduce buffering and break real-time progress without necessarily
breaking the initial connection.

## 11. Health Checks

- `GET /health` — process liveness only; never touches a dependency.
  Must not depend on Postgres/Redis/MinIO — a temporary outage in any of
  them must not cause an infinite container-restart loop from a
  liveness probe alone.
- `GET /ready` — verifies Postgres, Redis, and MinIO are reachable (each
  bounded to a few seconds). `200` when all are healthy, `503`
  otherwise, with a per-dependency `ok`/`error` breakdown — never a
  connection string, credential, or raw driver error.
- Every container in `docker-compose.prod.yml` has an explicit
  Docker-level healthcheck (`backend`/`frontend`'s images bake in their
  own via `HEALTHCHECK`; `postgres`/`redis`/`minio`/`reverse-proxy` are
  set at the Compose level) — except `migrate` (one-shot, an exit code
  is its own outcome signal) and `worker` (no HTTP endpoint to probe;
  its inherited image healthcheck is explicitly disabled — see that
  service's own comment in `docker-compose.prod.yml`). Operationally,
  "is the worker running" is `docker compose ps worker` / `docker
  compose logs worker` (look for `"job started"`/`"job completed"`
  lines) rather than a health endpoint.

## 12. Resource Limits

`docker-compose.prod.yml` sets `mem_limit`/`cpus` per service —
inspection-based starting points for a single-machine SIH demo, **not**
load-tested production guarantees:

| Service | Memory | CPU | Why |
|---|---|---|---|
| `backend` | 512m | 1.0 | Streams document hashing (never loads a whole file into memory — `pkg/hash` uses `io.Copy`), bcrypt (cost 12), JWT — no large in-memory working set expected under normal load. |
| `worker` | 256m | 0.5 | Fixed concurrency of 5 (`internal/jobs.serverConcurrency`) processing sequential, batched audit-chain reads. |
| `postgres` | 512m | — | Default `postgres:15` settings; tune `shared_buffers`/`work_mem` and this limit together for real data volumes. |
| `redis` | 320m (with a 256mb `maxmemory` + `noeviction`) | — | `noeviction`, not an LRU policy: Asynq's queued-but-unprocessed jobs live in Redis, and evicting them under memory pressure would silently drop work — refusing new writes instead is the only safe failure mode for a queue. |
| `minio` | 512m | — | Object storage; scale with actual evidence-file volume. |
| `frontend` | 128m | 0.5 | Static file serving only. |
| `reverse-proxy` | 128m | 0.5 | TLS termination + proxying; SSE connections are long-lived but hold minimal per-connection state. |

The application-level upload limit (`MAX_UPLOAD_SIZE`, default 50 MiB)
and the reverse proxy's `client_max_body_size`
(`NGINX_CLIENT_MAX_BODY_SIZE`, default 55 MiB) must be kept roughly in
sync — the smaller of the two wins, and a proxy limit smaller than the
application's would reject legitimate uploads before the API ever sees
them.

## 13. Logging

Structured JSON operational logs (`internal/logger`) — timestamp,
severity, request ID where applicable — are entirely separate from the
cryptographic audit trail (`audit_log`, System 10): operational logs are
for operators debugging the running service, never a substitute for the
tamper-evident compliance record. Confirmed (System 15) never to log
passwords, tokens, full `Authorization` headers, or evidence content.

`docker compose logs -f <service>` / `docker compose logs -f` (all
services) is the primary way to read them in this deployment. No log-
rotation configuration is added here — Docker's own default JSON-file
log driver already rotates by size in recent Docker versions; a real
production deployment should confirm its Docker daemon's
`log-opts`/`max-size` (or ship logs to an external aggregator) rather
than assume unbounded local log growth is fine.

## 14. Backup & Restore

**A PostgreSQL-only backup is not sufficient**: case/document metadata,
hashes, audit history, and user data live in PostgreSQL, but the raw
evidence bytes live in MinIO. `scripts/backup.sh` backs up both,
together, in one run — tested end-to-end (System 17): backed up a real
running deployment's database (`pg_dump -Fc`) and MinIO bucket (`mc
mirror`), then restored both into a completely fresh Postgres+MinIO
pair and confirmed identical row counts (`cases`, `documents`,
`audit_log`) and a byte-for-byte SHA-256 match between a restored
document's MinIO object and its stored hash in the restored database.

```bash
./scripts/backup.sh                       # -> ./backups/<UTC timestamp>/
./scripts/restore.sh ./backups/<ts> --verify-only   # non-destructive sanity check
./scripts/restore.sh ./backups/<ts>                 # full restore (destructive — prompts for confirmation)
```

**Restore prerequisite**: run `cmd/migrate up && cmd/migrate seed`
against the target database *before* restoring a backup into it — the
backup is a data dump, not a schema dump; the `evidentia_app` role and
schema must already exist for the restored grants/data to apply
cleanly (confirmed necessary while validating this exact flow).

**What `scripts/backup.sh` does NOT do** (see that script's own header
for the full list): encrypt the backup at rest, ship it off-host, run on
a schedule, or enforce a retention policy. For a real deployment, add:

- **Encryption**: pipe through `gpg`/`age`, or rely on disk/volume-level
  encryption at the backup destination.
- **Off-host storage**: `aws s3 sync`/`rsync`/etc. after the script
  completes, or point its output at an already-mounted remote
  filesystem.
- **Scheduling**, e.g. a crontab entry:
  ```
  0 2 * * * cd /path/to/evidentia && ./scripts/backup.sh >> /var/log/evidentia-backup.log 2>&1
  ```
- **Retention**: prune old `backups/<timestamp>/` directories on your
  own schedule (e.g. keep the last N).

## 15. Rollback

- **Application (backend/worker/frontend)**: redeploy the previous
  image tag/commit — `docker compose ... up -d --no-deps <service>`
  with the older image. Stateless beyond what's in Postgres/MinIO/Redis,
  so this alone is safe.
- **Database migrations**: **never** blindly run `cmd/migrate down` in
  production — a down migration can be destructive (dropping a column/
  table) in a way a forward migration adding it was not. Prefer
  forward-compatible schema changes (add a new nullable column instead
  of altering one in place, backfill, then a *later* migration removes
  the old one once nothing reads it) so rolling the application back
  never requires rolling the schema back too. If a migration truly must
  be reverted, restore from the pre-migration backup (section 14)
  instead of trusting an automated `down` script against live data.
- **Configuration**: keep the previous `.env`/Compose file alongside the
  new one under version control (or your secret manager's own history)
  so reverting is "redeploy the old file," not "reconstruct it from
  memory."
- **Redis/queue state**: Asynq retries failed jobs automatically within
  their configured attempt limit; a rollback does not need to manually
  drain or replay the queue unless you are also rolling back a change to
  the job payload's own shape.

## 16. Production Configuration Validation

`internal/config` fails startup (never a partial/degraded start) if a
required credential is empty, `JWT_SIGNING_KEY` is under 32 characters,
`CORS_ALLOWED_ORIGINS` contains `*` in production or combined with
`CORS_ALLOW_CREDENTIALS=true` in *any* environment, or several other
range/format checks — see `internal/config/validate.go`. `APP_ENV`
actually changes behavior (Gin release mode, stricter CORS validation),
not merely a label — see `internal/config/config.go`'s `IsProduction()`.

## 17. Container Security Review (System 17)

- **Non-root**: `backend`/`worker`/`migrate` run as a dedicated
  `evidentia` user (backend/Dockerfile); `frontend`/`reverse-proxy` run
  as `nginxinc/nginx-unprivileged`'s non-root user. Only `postgres`,
  `redis`, and `minio` (upstream, unmodified images) run their own
  default user — not customized here, consistent with how those images
  are normally deployed.
- **No privileged containers, no host networking, no extra Linux
  capabilities added** anywhere in either Compose file.
- **No host filesystem mounts** beyond the reverse proxy's own config/
  certificate bind mounts (both explicitly `:ro`) and the named
  volumes for Postgres/MinIO/Redis data.
- **Minimal images**: `alpine:3.19` (backend), `node:22-alpine` build
  stage discarded before runtime (frontend), `nginxinc/nginx-
  unprivileged:1.27-alpine` — no Go/Node toolchain, no source code, in
  any runtime image.
- **No management ports exposed**: MinIO's console/API and Postgres/
  Redis are unreachable from outside the deployment entirely in
  `docker-compose.prod.yml` (see that file's own header comment) —
  reachable only via `docker compose exec`/a temporary port-forward if
  an operator genuinely needs interactive access.

## 18. Troubleshooting / Common Failures

- **`backend`/`worker` won't start, log `bootstrap admin: load ADMIN
  role: no rows in result set`**: the reference-data seed hasn't been
  applied. Run `cmd/migrate seed` (or, in `docker-compose.prod.yml`,
  confirm the `migrate` service actually completed — `docker compose
  logs migrate`).
- **`reverse-proxy` restart-loops, logs `cannot load certificate key ...
  Permission denied`**: the mounted `privkey.pem` isn't readable by the
  container's non-root user — see section 9's permissions note.
- **A container's healthcheck fails with `wget: can't connect ... /
  Connection refused` even though the process looks like it started**:
  on the Nginx-based images used here, `/etc/hosts` resolves `localhost`
  to `::1` (IPv6) before `127.0.0.1`, but this deployment's Nginx
  configs only listen on IPv4 — use `127.0.0.1` explicitly in any
  healthcheck/script you add against these images (already done
  throughout `frontend/Dockerfile` and `docker-compose.prod.yml` — this
  is documented here specifically so a future addition doesn't
  reintroduce it).
- **`docker compose ... config`/`up` fails with `service "reverse-proxy"
  depends on undefined service "frontend"`**: the `frontend` Compose
  profile isn't active — `export COMPOSE_PROFILES=frontend` (or
  `--profile frontend`) before running any `docker compose ... -f
  docker-compose.prod.yml` command. See docker-compose.prod.yml's header
  comment for why an override file cannot activate this on its own
  (Compose merges `profiles:` as a union across files, never a
  replacement).
- **Removing a host port from a service via `docker-compose.prod.yml`
  doesn't seem to work**: Compose merges a re-specified `ports:` list as
  a union across files, never a replacement — an override genuinely
  cannot remove a port the base file publishes (confirmed while building
  this exact deployment). This is why `docker-compose.yml` itself
  publishes no ports at all for `backend`/`postgres`/`redis`/`minio`/
  `frontend`, and `docker-compose.override.yml` (auto-loaded only when
  no `-f` flag is given) is the sole place local-dev convenience ports
  live.

## 19. What This Is NOT

This document and `docker-compose.prod.yml` give you a genuinely
working, validated, production-*like* deployment — not a certification
that it is ready for real judicial/investigative production traffic
without further review. Specifically still missing for a real
deployment:

- Tested automated TLS certificate issuance/renewal for a real domain
  (self-signed/manually-supplied certificates only are validated here).
- Horizontal scaling guidance (multiple `backend`/`worker` replicas
  behind a load balancer) — the architecture does not preclude it
  (Asynq safely supports multiple concurrent worker consumers of the
  same queue), but it is not configured or load-tested here.
- An external log aggregator, metrics/alerting stack, or automated
  backup scheduling/off-host shipping/encryption (see section 14).
- A disaster-recovery runbook beyond "restore from the last backup" —
  RTO/RPO targets are deployment-specific decisions this repository
  cannot make for you.
