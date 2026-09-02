# Evidentia

Evidentia is a secure digital evidence and case-management platform designed for
investigative and judicial workflows.

> **Status:** Scaffolding phase. This repository currently contains project
> structure, placeholders, and documentation skeletons only. No application
> logic has been implemented yet.

## Repository Layout

```text
evidentia/
├── backend/     # Go backend (API, services, database, jobs, storage)
├── frontend/    # Future frontend application (placeholder only)
├── docs/        # Project documentation
└── scripts/     # Repo-level helper scripts
```

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

> TODO: Setup instructions will be added once implementation begins.

## Project Status

This phase covers **backend scaffolding only**:

- Directory structure
- Placeholder files with TODO responsibilities
- Configuration skeletons (`.env.example`, `sqlc.yaml`, Docker, Makefile)
- Documentation scaffolding

No authentication, authorization, database, storage, cryptography, audit, or
job-processing logic has been implemented. See [ARCHITECTURE.md](./ARCHITECTURE.md)
for the intended design.

## License

See [LICENSE](./LICENSE).
