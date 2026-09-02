# Evidentia

Evidentia is a secure digital evidence and case-management platform designed for
investigative and judicial workflows.

> **Status:** Backend System 1 — Foundation & Infrastructure — is
> implemented: configuration, structured logging, PostgreSQL/Redis/MinIO
> connectivity, the HTTP server (Gin) with its middleware stack, and
> `/health` + `/ready`. Authentication, authorization, case management,
> documents, and the audit chain are not implemented yet — those are later
> systems; see [ARCHITECTURE.md](./ARCHITECTURE.md). The `frontend/`
> directory contains an Angular application (generated via Angular CLI)
> plus design reference material.

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
cp .env.example .env      # edit POSTGRES_PASSWORD / MINIO_ROOT_PASSWORD
docker compose up -d
curl http://localhost:8080/health
curl http://localhost:8080/ready
```

### Backend directly on the host

```bash
cd backend
cp .env.example .env      # edit DATABASE_*/MINIO_* credentials
# start postgres, redis, minio however you prefer (or via the root
# docker-compose.yml, omitting the backend service)
go mod download
go run ./cmd/server
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

Not yet implemented (later systems): authentication, RBAC/ABAC, PostgreSQL
RLS, case management, document upload/hashing/redaction, the audit chain,
compliance certificates, and background jobs. See
[ARCHITECTURE.md](./ARCHITECTURE.md) for the full intended design.

### Frontend

An Angular application lives under `frontend/`. Landing-page design
reference material (mockups and prompts) lives under
`Landing page UI mockups/`.

## License

See [LICENSE](./LICENSE).
