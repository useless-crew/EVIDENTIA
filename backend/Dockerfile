# Evidentia Backend — Production Dockerfile
#
# Multi-stage build: compile static binaries in a full Go toolchain image,
# then run them in a minimal, non-root runtime image with no build tools.
#
# One image, three entrypoints — never three Dockerfiles duplicating the
# same build recipe, and never a second implementation of any business
# logic (each binary is a thin composition root over the same internal/
# packages):
#   /app/evidentia — cmd/server: the HTTP API (+ embedded worker by
#     default; see cmd/server/main.go's DISABLE_EMBEDDED_WORKER doc
#     comment). Default ENTRYPOINT — every existing docker-compose.yml
#     service and local `docker run` invocation is unaffected.
#   /app/migrate   — cmd/migrate: golang-migrate up/down/version/seed.
#     Used as docker-compose.prod.yml's one-shot `migrate` service
#     (System 17 — see that file). Needs db/migrations/ and db/seed/
#     alongside it (copied below), since it resolves those paths
#     relative to its own working directory.
#   /app/worker    — cmd/worker: the standalone Asynq worker, for
#     docker-compose.prod.yml's separate `worker` service.

# ---- Build stage ----
FROM golang:1.25-alpine AS builder

WORKDIR /src

# Cache module downloads separately from source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -tags swaggerui -trimpath -ldflags="-s -w" -o /out/evidentia ./cmd/server
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

# ---- Runtime stage ----
FROM alpine:3.19 AS runtime

RUN apk add --no-cache ca-certificates && \
    addgroup -S evidentia && adduser -S evidentia -G evidentia

WORKDIR /app
COPY --from=builder /out/evidentia /app/evidentia
COPY --from=builder /out/migrate /app/migrate
COPY --from=builder /out/worker /app/worker
COPY --from=builder /src/db/migrations /app/db/migrations
COPY --from=builder /src/db/seed /app/db/seed

USER evidentia

EXPOSE 8080

# Meaningful only for the default (server) entrypoint — docker-compose.yml/
# .prod.yml's own `healthcheck:` on the migrate/worker services (which
# don't serve HTTP) overrides or omits this per service; Compose's
# service-level healthcheck always takes precedence over a Dockerfile
# HEALTHCHECK when both are present.
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=5 \
    CMD wget -qO- http://localhost:8080/health || exit 1

ENTRYPOINT ["/app/evidentia"]
