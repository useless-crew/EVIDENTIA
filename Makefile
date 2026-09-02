# Evidentia — Root Developer Commands
#
# The Go module lives in backend/ (kept there deliberately — see
# backend/go.mod and TECH_STACK.md). Go tooling only searches parent
# directories for a go.mod, never subdirectories, so plain `go test ./...`
# run from the repository root fails with "go.mod file not found" — that
# is expected Go behavior, not a bug to route around by moving the module.
#
# These targets exist so `make <target>` works from the repository root
# without requiring `cd backend` first: each Go-tooling target below
# delegates to backend/Makefile (the single source of truth for how each
# command is actually invoked), rather than duplicating its recipes here.

.PHONY: dev run build test test-race vet fmt lint migrate-up migrate-down sqlc seed swagger clean docker-up docker-down docker-logs

dev run build test test-race vet fmt lint migrate-up migrate-down sqlc seed swagger clean:
	$(MAKE) -C backend $@

docker-up:
	docker compose up -d --build

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f backend
