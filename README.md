# Evidentia

Evidentia is a secure digital evidence and case-management platform designed for
investigative and judicial workflows.

> **Status:** Backend Systems 1-7 are implemented: foundation/
> infrastructure, the full database schema with Row-Level Security, JWT +
> refresh-token authentication, centralized RBAC/ABAC authorization
> composed with Row-Level Security as defense-in-depth, case management,
> document upload/download, and document integrity verification +
> compliance certificates. The audit hash chain, redaction, document
> sharing, and user administration are not implemented yet — those are
> later systems; see [ARCHITECTURE.md](./ARCHITECTURE.md). The
> `frontend/` directory contains an Angular application connected to this
> backend (real login, cases, documents, verify/certificate — see
> [frontend/README.md](./frontend/README.md)); a few screens with no
> backend counterpart yet (audit log, admin, redaction) remain
> illustrative mock content, clearly marked as such in
> `frontend/src/app/core/services/dms-state.service.ts`.

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

### Full stack, backend + frontend, for local development

```bash
docker compose up -d postgres redis minio
cd backend && DATABASE_MIGRATOR_USER=evidentia DATABASE_MIGRATOR_PASSWORD=changeme_example \
  DATABASE_HOST=localhost DATABASE_NAME=evidentia go run ./cmd/migrate up
./scripts/seed_db.sh   # or: cd backend && ../scripts/seed_db.sh
cd .. && docker compose up -d backend   # http://localhost:8080

# create at least one login-able account — see frontend/README.md's
# "Demo login accounts" for why this step exists and full role list
cd backend && DATABASE_MIGRATOR_USER=evidentia DATABASE_MIGRATOR_PASSWORD=changeme_example \
  DATABASE_HOST=localhost DATABASE_NAME=evidentia \
  go run ./cmd/devuser -email=police@example.test -password=at-least-8-chars -first=Jane -last=Doe -role=POLICE

cd ../frontend && npm install && npm start   # http://localhost:4200
```

Sign in at `http://localhost:4200/login` with the account just created
(or one of the other roles — see frontend/README.md). See
[frontend/README.md](./frontend/README.md) for what the frontend actually
connects to and its environment configuration.

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
[docs/SECURITY.md](./docs/SECURITY.md)'s Authorization section. It guards
every route Systems 5-7 registered below; the audit-chain writer/verifier,
redaction, document sharing, and user administration remain later
systems' scope. See [ARCHITECTURE.md](./ARCHITECTURE.md) for the full
intended design and [docs/DATABASE_SCHEMA.md](./docs/DATABASE_SCHEMA.md)
for the full schema.

**Systems 5-7 — Case Management, Document Management, and Evidence
Verification & Compliance Certificates** are implemented:

- `POST/GET /cases`, `GET/PUT /cases/:id` — role-scoped listing
  (PostgreSQL RLS), status lifecycle, involved parties, timeline
- `POST /cases/:id/documents` (streaming upload + SHA-256), `GET
  /documents/:id/download` — evidence ingestion/retrieval, MinIO-backed
- `POST /documents/:id/verify` — recomputes SHA-256 from the actual
  stored object and compares it to the canonical hash; a mismatch is
  reported, never silently repaired
- `GET /documents/:id/certificate` — a compliance certificate
  cryptographically bound (ECDSA P-256) to the exact verified hash; never
  issued for a document that fails verification

See [docs/API_ENDPOINTS.md](./docs/API_ENDPOINTS.md) for the full
request/response contracts and [docs/SECURITY.md](./docs/SECURITY.md)'s
"Document Verification & Compliance Certificates" section for the
security design.

### Frontend

An Angular application lives under `frontend/`, connected to the real
backend above (real login/session handling, case list/detail/creation,
document upload/download, and document verification/compliance
certificates — see [frontend/README.md](./frontend/README.md) for the
integration layer and environment configuration). Landing-page design
reference material (mockups and prompts) lives under
`Landing page UI mockups/`.

## License

See [LICENSE](./LICENSE).
