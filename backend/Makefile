# Evidentia Backend — Developer Commands
#
# Every target here is functional today. Targets for tooling this system
# doesn't use yet (migrations, sqlc, swagger) are intentionally omitted
# rather than stubbed out — they'll be added, working, alongside the
# systems that need them (schema/migrations, sqlc queries, API
# annotations), instead of echoing "TODO" in the meantime.

.PHONY: dev run build test test-race vet fmt lint docker-up docker-down docker-logs clean

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
