# Evidentia Backend — Production Dockerfile
#
# Multi-stage build: compile a static binary in a full Go toolchain image,
# then run it in a minimal, non-root runtime image with no build tools.

# ---- Build stage ----
FROM golang:1.25-alpine AS builder

WORKDIR /src

# Cache module downloads separately from source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/evidentia ./cmd/server

# ---- Runtime stage ----
FROM alpine:3.19 AS runtime

RUN apk add --no-cache ca-certificates && \
    addgroup -S evidentia && adduser -S evidentia -G evidentia

WORKDIR /app
COPY --from=builder /out/evidentia /app/evidentia

USER evidentia

EXPOSE 8080

ENTRYPOINT ["/app/evidentia"]
