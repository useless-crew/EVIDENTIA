# Deployment

## Purpose

How to run Evidentia's backend locally today (System 1 — Foundation &
Infrastructure). Production deployment (TLS termination, secrets
management, backups, scaling) is out of scope until a later system.

## Local Development — Docker Compose

```bash
docker compose up -d
```

No setup required — every credential (`POSTGRES_PASSWORD`,
`MINIO_ROOT_PASSWORD`, ...) falls back to an obvious placeholder
(`changeme_example`) documented in `.env.example`. This is a convenience
for local orchestration only, not a relaxation of "never default
credentials": `internal/config` still refuses to start the Go application
itself if its own credential variables are empty (see Environment
Configuration below). To use real credentials for the containers:

```bash
cp .env.example .env
# edit POSTGRES_PASSWORD and MINIO_ROOT_PASSWORD
docker compose up -d
```

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
# from the root docker-compose.yml (postgres/redis/minio only)
go mod download
go run ./cmd/server
```

Or with `make` (from `backend/`, or from the repository root — the root
`Makefile` delegates each of these to `backend/Makefile`, since the Go
module lives in `backend/` and plain `go test ./...` etc. only resolve
from inside it): `make run`, `make build`, `make test`, `make test-race`,
`make vet`, `make fmt`, `make docker-up` / `docker-down` (root Makefile
runs `docker compose` directly; backend Makefile wraps `../docker-compose.yml`),
`make clean`. `make lint` requires
[golangci-lint](https://golangci-lint.run/welcome/install/) to be installed
separately.

## Environment Configuration

Two files, two audiences:

- [`../.env.example`](../.env.example) — variables `docker-compose.yml`
  substitutes into the containers it manages (credentials, ports).
- [`../backend/.env.example`](../backend/.env.example) — every variable
  `internal/config` reads when running the Go binary directly. Values with
  no default (`DATABASE_USER`, `DATABASE_PASSWORD`, `DATABASE_NAME`,
  `MINIO_ACCESS_KEY`, `MINIO_SECRET_KEY`, `MINIO_BUCKET`) must be set
  explicitly — the process refuses to start otherwise. See
  [`../ARCHITECTURE.md`](../ARCHITECTURE.md) and `internal/config/config.go`
  for the full list and validation rules.

## Health and Readiness

- `GET /health` — process liveness only; always cheap, never touches a
  dependency. `{"status":"ok","service":"evidentia-backend","version":"..."}`
- `GET /ready` — verifies PostgreSQL, Redis, and MinIO are reachable.
  Returns `200` when all are healthy, `503` otherwise, with a
  per-dependency breakdown:
  `{"status":"ready","dependencies":{"postgres":"ok","redis":"ok","minio":"ok"}}`

## Database Migrations

Not yet applicable — no schema exists yet (`backend/db/migrations` holds
only a placeholder). `golang-migrate` will be wired up once the system that
owns the schema is implemented.

## Building the Backend

`backend/Dockerfile` is a two-stage build: `golang:1.25-alpine` compiles a
static binary, which then runs as a non-root user on `alpine:3.19` — no Go
toolchain or build tools in the final image. Build it directly with
`docker build ./backend` or via `docker compose build backend`.

## Production Considerations

Not implemented yet: TLS termination, secrets management (beyond
environment variables), backup/restore, and horizontal scaling guidance.
These will be documented once the systems they depend on (auth, RLS,
audit chain) exist.
