# Evidentia

Evidentia is a secure digital evidence and case-management platform designed for
investigative and judicial workflows.

> **Status:** Backend scaffolding phase. The `backend/` directory currently
> contains project structure, placeholders, and documentation skeletons only
> — no application logic has been implemented yet. The `frontend/` directory
> contains an Angular application (generated via Angular CLI) plus design
> reference material.

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

> TODO: Backend setup instructions will be added once implementation begins.
> For the frontend, see [frontend/README.md](./frontend/README.md).

## Project Status

### Backend

This phase covers **backend scaffolding only**:

- Directory structure
- Placeholder files with TODO responsibilities
- Configuration skeletons (`.env.example`, `sqlc.yaml`, Docker, Makefile)
- Documentation scaffolding

No authentication, authorization, database, storage, cryptography, audit, or
job-processing logic has been implemented. See [ARCHITECTURE.md](./ARCHITECTURE.md)
for the intended design.

### Frontend

An Angular application lives under `frontend/`. Landing-page design
reference material (mockups and prompts) lives under
`Landing page UI mockups/`.

## License

See [LICENSE](./LICENSE).
