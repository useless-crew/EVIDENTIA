# Evidentia Backend — Dockerfile Scaffold
#
# STRUCTURAL SKELETON ONLY. Does not implement a working build/runtime yet
# because cmd/server/main.go has no runnable application logic.
#
# TODO: Finalize multi-stage build once the backend compiles and runs.

# ---- Build stage ----
FROM golang:1.22 AS builder

WORKDIR /app

# TODO: copy go.mod/go.sum and run `go mod download` once dependencies exist.
# COPY go.mod go.sum ./
# RUN go mod download

COPY . .

# TODO: uncomment once main.go has a real entrypoint.
# RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/evidentia ./cmd/server

# ---- Runtime stage ----
FROM alpine:3.19 AS runtime

WORKDIR /app

# TODO: copy compiled binary once build stage produces one.
# COPY --from=builder /bin/evidentia /app/evidentia

EXPOSE 8080

# TODO: define ENTRYPOINT/CMD once the binary exists.
# ENTRYPOINT ["/app/evidentia"]
