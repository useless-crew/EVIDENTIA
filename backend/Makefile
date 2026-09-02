# Evidentia Backend — Developer Commands
#
# Every target here is functional today.
#
# migrate-up/migrate-down require DATABASE_MIGRATOR_USER/PASSWORD (a
# privileged, schema-owning role — never the app's own DATABASE_USER/
# PASSWORD, which is the least-privilege evidentia_app role). seed requires
# the same. See docs/DATABASE_SCHEMA.md.

.PHONY: dev run build test test-race vet fmt lint migrate-up migrate-down sqlc seed swagger docker-up docker-down docker-logs clean

dev: run

run:
	go run ./cmd/server

build:
	go build -trimpath -o bin/evidentia ./cmd/server

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

migrate-up:
	go run ./cmd/migrate up

migrate-down:
	go run ./cmd/migrate down

# Requires the sqlc CLI (https://docs.sqlc.dev/en/latest/overview/install.html)
# — not part of this project's approved dependency stack (it's a code
# generator, not an imported library), so it is not installed automatically.
sqlc:
	@command -v sqlc >/dev/null 2>&1 || { \
		echo "sqlc is not installed. See https://docs.sqlc.dev/en/latest/overview/install.html"; \
		exit 1; \
	}
	sqlc generate

seed:
	./scripts/seed_db.sh

# Requires the swag CLI (https://github.com/swaggo/swag#getting-started)
# — a code generator, not part of the imported dependency stack (the
# runtime github.com/swaggo/swag registration package it generates code
# against IS a real go.mod dependency; the CLI itself is not).
swagger:
	@command -v swag >/dev/null 2>&1 || { \
		echo "swag is not installed. See https://github.com/swaggo/swag#getting-started"; \
		exit 1; \
	}
	swag init -g cmd/server/main.go -o docs/swagger --parseDependency --parseInternal

# Requires golangci-lint (https://golangci-lint.run/welcome/install/) —
# not part of this project's approved dependency stack, so it is not
# installed automatically. Install it yourself to use this target.
lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint is not installed. See https://golangci-lint.run/welcome/install/"; \
		exit 1; \
	}
	golangci-lint run ./...

docker-up:
	docker compose -f ../docker-compose.yml up -d --build

docker-down:
	docker compose -f ../docker-compose.yml down

docker-logs:
	docker compose -f ../docker-compose.yml logs -f backend

clean:
	rm -rf bin/ tmp/ coverage.out
