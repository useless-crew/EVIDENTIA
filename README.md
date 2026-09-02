# Evidentia

Evidentia is a secure digital evidence and case-management platform designed for
investigative and judicial workflows.

> **Status:** Backend Systems 1-4 are implemented: foundation/
> infrastructure, the full database schema with Row-Level Security, JWT +
> refresh-token authentication with rotation/reuse detection, and
> centralized RBAC/ABAC authorization composed with that Row-Level
> Security as defense-in-depth. Case/document HTTP handlers and the
> audit-chain writer are not implemented yet — those are later systems; see
> [ARCHITECTURE.md](./ARCHITECTURE.md). The `frontend/` directory contains
> an Angular application (generated via Angular CLI) plus design reference
> material.

## Repository Layout

```text
evidentia/
├── backend/     # Go backend (API, services, database, jobs, storage)
├── frontend/    # Angular frontend application
├── docs/        # Project documentation
└── scripts/     # Repo-level helper scripts
```

See [frontend/README.md](./frontend/README.md) for frontend development
commands (`ng serve`, `ng build`, `ng test`, etc.).

## Documentation

- [TECH_STACK.md](./TECH_STACK.md) — Authoritative technology stack
- [ARCHITECTURE.md](./ARCHITECTURE.md) — High-level system architecture
- [docs/API_ENDPOINTS.md](./docs/API_ENDPOINTS.md)
- [docs/DATABASE_SCHEMA.md](./docs/DATABASE_SCHEMA.md)
- [docs/SECURITY.md](./docs/SECURITY.md)
- [docs/AUDIT_CHAIN.md](./docs/AUDIT_CHAIN.md)
- [docs/STORAGE.md](./docs/STORAGE.md)
- [docs/DEPLOYMENT.md](./docs/DEPLOYMENT.md)

## Getting Started

### Full stack via Docker Compose

```bash
docker compose up -d
curl http://localhost:8080/health
curl http://localhost:8080/ready
```

Works with no setup — every credential falls back to a documented
placeholder (see `.env.example`). To use real credentials instead:
`cp .env.example .env`, edit it, then `docker compose up -d` again.

### Backend directly on the host

```bash
cd backend
cp .env.example .env      # edit DATABASE_*/MINIO_* credentials
# start postgres, redis, minio however you prefer (or via the root
# docker-compose.yml, omitting the backend service)
go mod download
go run ./cmd/server
```

### Make targets (from the repository root)

The Go module lives in `backend/`, so plain `go test ./...` etc. only work
from inside it — but `make <target>` works from the repository root
without `cd backend` first (see the root `Makefile`, which delegates to
`backend/Makefile`):

```bash
make build          # -> backend/bin/evidentia
make test
make test-race
make vet
make fmt
make migrate-up      # needs DATABASE_MIGRATOR_USER/PASSWORD — see docs/DATABASE_SCHEMA.md
make migrate-down
make seed            # roles/permissions reference data
make sqlc            # regenerate backend/db/generated (requires the sqlc CLI)
make swagger         # regenerate backend/docs/swagger (requires the swag CLI)
make docker-up       # docker compose up -d --build
make docker-down
```

See [docs/DEPLOYMENT.md](./docs/DEPLOYMENT.md) for full details, and
[frontend/README.md](./frontend/README.md) for frontend development
commands.

## Project Status

### Backend

**System 1 — Foundation & Infrastructure** is implemented:

- Typed, validated configuration loaded from environment variables
  (fails startup on missing/invalid values — see `internal/config`)
- Structured JSON/text logging (`internal/logger`)
- PostgreSQL (`internal/database`), Redis (`internal/cache`), and MinIO
  (`internal/storage`) connectivity with health checks
- Dependency-injected application container (`internal/app`) — no global
  mutable connections
- Gin HTTP server with request ID, CORS, structured request logging, panic
  recovery, and body-size-limit middleware (`internal/httpserver`,
  `internal/middleware`)
- `GET /health` and `GET /ready` (`internal/handlers/health`)
- Graceful shutdown on SIGINT/SIGTERM
- Multi-stage Dockerfile and Docker Compose with health-checked services
- Unit, race-safe, and integration (`-tags=integration`) tests

**System 2 — Database & Data Layer** is implemented:

- Full PostgreSQL schema (12 tables), versioned via `golang-migrate`
  (`backend/db/migrations/`, run via `cmd/migrate`)
- Row-Level Security on every case/document/audit-adjacent table —
  transaction-local identity (`internal/repository.WithTx`), fail-closed,
  verified by integration test, not just declared
- Audit log enforced append-only at the database level: the runtime role
  holds `SELECT`+`INSERT` only, no `UPDATE`/`DELETE`, verified by
  integration test
- Least-privilege role separation: a privileged migrator role vs. the
  `evidentia_app` runtime role, which owns nothing
- `sqlc`-generated, type-safe query code (`backend/db/generated/`) behind
  a thin repository layer (`internal/repository`)
- Idempotent reference-data seeding (roles/permissions —
  `backend/scripts/seed_db.sh`)

**System 3 — Authentication & Session Security** is implemented:

- `POST /api/v1/auth/{login,refresh,logout}` — bcrypt password hashing,
  HS256 JWT access tokens (15 min default), opaque high-entropy refresh
  tokens (7 day default) stored only as a SHA-256 hash
- Refresh-token rotation with reuse detection: an already-rotated token
  presented again revokes its entire session family, verified by the
  exact replay scenario as an integration test
- Every authenticated request re-resolves current account status/roles
  from the database (`internal/middleware.Auth`) — a deactivated user's
  still-unexpired access token is rejected on the next request
- Generic, enumeration-resistant failures: unknown email, wrong password,
  and inactive account all return an identical error
- Failed/successful authentication recorded via an `audit.Recorder`
  interface (operational logging today; System 8 provides the durable,
  hash-chained implementation later with no change to auth code)

RBAC/ABAC authorization (System 4, `internal/authz`) is implemented:
centralized permission checks, case/document attribute-based access
control, IDOR prevention, and privilege-escalation guards, composed with
System 2's Row-Level Security rather than replacing it — see
[docs/SECURITY.md](./docs/SECURITY.md)'s Authorization section. It has no
routes to guard yet, though: case/document HTTP handlers, document
upload/hashing/redaction, the audit-chain writer/verifier, compliance
certificate generation, and background jobs remain later systems' scope.
See [ARCHITECTURE.md](./ARCHITECTURE.md) for the full intended design and
[docs/DATABASE_SCHEMA.md](./docs/DATABASE_SCHEMA.md) for the full schema.

### Frontend

An Angular application lives under `frontend/`. Landing-page design
reference material (mockups and prompts) lives under
`Landing page UI mockups/`.

## License

See [LICENSE](./LICENSE).
