# Deployment

## Purpose

How to run Evidentia's backend locally today (Systems 1-3: Foundation &
Infrastructure, Database & Data Layer, Authentication & Session
Security). Production deployment (TLS termination, secrets management,
backups, scaling) is out of scope until a later system.

## Local Development — Docker Compose

```bash
docker compose up -d postgres redis minio
cd backend
DATABASE_MIGRATOR_USER=evidentia DATABASE_MIGRATOR_PASSWORD=changeme_example \
  DATABASE_HOST=localhost DATABASE_NAME=evidentia go run ./cmd/migrate up
cd ..
docker compose up -d
```

No setup beyond that migration step required — every credential
(`POSTGRES_PASSWORD`, `MINIO_ROOT_PASSWORD`, `JWT_SIGNING_KEY`, ...) falls
back to an obvious placeholder (`changeme_example`-based) documented in
`.env.example`. This is a convenience for local orchestration only, not a
relaxation of "never default credentials": `internal/config` still
refuses to start the Go application itself if its own credential
variables are empty (see Environment Configuration below). To use real
credentials for the containers:

```bash
cp .env.example .env
# edit POSTGRES_PASSWORD, MINIO_ROOT_PASSWORD, JWT_SIGNING_KEY
docker compose up -d
```

**Why the migration step is required**: the `backend` container connects
to PostgreSQL as `evidentia_app` — a least-privilege role the migration
itself creates (see docs/DATABASE_SCHEMA.md). Without it, `backend` fails
to start (fails closed, as designed) rather than falling back to a
privileged connection. It only needs to be run once per `postgres_data`
volume — `go run ./cmd/migrate up` is safe to run again later (no-op if
already applied).

This starts `postgres`, `redis`, `minio`, and `backend`, each with a health
check; `backend` waits for the other three to report healthy before it
starts. Verify:

```bash
curl http://localhost:8080/health
curl http://localhost:8080/ready
docker compose logs -f backend
docker compose down          # stops containers, keeps volumes
```

The frontend is not started by default (`docker compose up` skips it via a
compose profile) — see [../frontend/README.md](../frontend/README.md).

## Local Development — Backend on the host

```bash
cd backend
cp .env.example .env
# point DATABASE_*/REDIS_ADDR/MINIO_* at running instances — e.g. the ones
# from the root docker-compose.yml (postgres/redis/minio only) — and set
# DATABASE_MIGRATOR_USER/PASSWORD, JWT_SIGNING_KEY
go mod download
go run ./cmd/migrate up   # once per fresh database
./scripts/seed_db.sh       # optional: roles/permissions reference data
go run ./cmd/server
```

Or with `make` (from `backend/`, or from the repository root — the root
`Makefile` delegates each of these to `backend/Makefile`, since the Go
module lives in `backend/` and plain `go test ./...` etc. only resolve
from inside it): `make run`, `make build`, `make test`, `make test-race`,
`make vet`, `make fmt`, `make migrate-up` / `migrate-down`, `make seed`,
`make sqlc` (requires the sqlc CLI), `make swagger` (requires the swag
CLI), `make docker-up` / `docker-down` (root Makefile runs `docker
compose` directly; backend Makefile wraps `../docker-compose.yml`), `make
clean`. `make lint` requires
[golangci-lint](https://golangci-lint.run/welcome/install/) to be installed
separately.

## Environment Configuration

Two files, two audiences:

- [`../.env.example`](../.env.example) — variables `docker-compose.yml`
  substitutes into the containers it manages (credentials, ports).
- [`../backend/.env.example`](../backend/.env.example) — every variable
  `internal/config` reads when running the Go binary directly. Values with
  no default (`DATABASE_USER`, `DATABASE_PASSWORD`, `DATABASE_NAME`,
  `MINIO_ACCESS_KEY`, `MINIO_SECRET_KEY`, `MINIO_BUCKET`, `JWT_SIGNING_KEY`)
  must be set explicitly — the process refuses to start otherwise. See
  [`../ARCHITECTURE.md`](../ARCHITECTURE.md) and `internal/config/config.go`
  for the full list and validation rules.

`DATABASE_MIGRATOR_USER`/`PASSWORD` are read separately by `cmd/migrate`
(via `config.LoadMigrator`, not the main `config.Load`) — a privileged,
schema-owning role, distinct from `DATABASE_USER`/`PASSWORD` (the
least-privilege `evidentia_app` role the server itself connects as). See
docs/DATABASE_SCHEMA.md.

## Health and Readiness

- `GET /health` — process liveness only; always cheap, never touches a
  dependency. `{"status":"ok","service":"evidentia-backend","version":"..."}`
- `GET /ready` — verifies PostgreSQL, Redis, and MinIO are reachable.
  Returns `200` when all are healthy, `503` otherwise, with a
  per-dependency breakdown:
  `{"status":"ready","dependencies":{"postgres":"ok","redis":"ok","minio":"ok"}}`

## Database Migrations

`backend/db/migrations/`, versioned via `golang-migrate` (used as a Go
library — `cmd/migrate`, not a separately installed CLI):

```bash
cd backend
DATABASE_MIGRATOR_USER=... DATABASE_MIGRATOR_PASSWORD=... go run ./cmd/migrate up
# or: make migrate-up / migrate-down (from backend/ or the repo root)
```

Two migrations today: `000001_init_schema` (System 2 — the full domain
schema, RLS, the `evidentia_app` role) and `000002_auth_sessions` (System
3 — refresh-token session storage). See docs/DATABASE_SCHEMA.md for the
full schema and design decisions.

## Reference-Data Seeding

`backend/scripts/seed_db.sh` (or `make seed`) applies
`backend/db/seed/*.sql` — the fixed role catalog (ADMIN/POLICE/FORENSICS/
LAWYER/JUDGE), the permission catalog, and a starting role→permission
mapping. Idempotent (safe to run more than once). Uses
`DATABASE_MIGRATOR_USER`/`PASSWORD`, same as migrations — seeding
`role_permissions` needs them (`evidentia_app` only has `SELECT` there).
Does **not** create any user account — see docs/DATABASE_SCHEMA.md for why.

## Running Integration Tests

```bash
cd backend
go test -tags=integration -p 1 ./...
```

Requires the docker-compose `postgres`/`redis`/`minio` services up and
migrated (above). **The `-p 1` is required**, not optional, when running
integration tests across multiple packages in one invocation: several
packages (`internal/service`, `internal/httpserver`, `backend/tests`)
truncate and repopulate shared tables in the same live database, and Go
runs different packages' test binaries concurrently by default — without
`-p 1` they reliably stomp on each other's fixtures (confirmed while
developing System 3). Running a single package's tests alone
(`go test -tags=integration ./internal/service/...`) does not need it.

## Building the Backend

`backend/Dockerfile` is a two-stage build: `golang:1.25-alpine` compiles a
static binary, which then runs as a non-root user on `alpine:3.19` — no Go
toolchain or build tools in the final image. Build it directly with
`docker build ./backend` or via `docker compose build backend`.

## Production Considerations

Not implemented yet: TLS termination, secrets management (beyond
environment variables), backup/restore, and horizontal scaling guidance.
These will be documented once the systems they depend on (RBAC/ABAC,
audit chain) exist.
