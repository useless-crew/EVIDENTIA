# Evidentia Frontend — Production Dockerfile (System 17)
#
# Multi-stage build: compile the Angular production bundle in a Node
# image, then serve only the resulting static files from a minimal,
# non-root Nginx image — no Node/npm, no source code, no Angular CLI in
# the runtime image.
#
# apiBaseUrl for this build is '/api/v1' (see src/environments/
# environment.ts) — same-origin, relative. This image serves ONLY static
# files; it never talks to the backend directly. In production, the
# reverse-proxy (see ../ops/reverse-proxy/) is what actually routes
# /api/v1/* to the backend and everything else to this container — see
# ../docker-compose.prod.yml.

# ---- Build stage ----
FROM node:22-alpine AS builder

WORKDIR /src

# Cache npm install separately from source changes.
COPY package.json package-lock.json ./
RUN npm ci

COPY . .

# `ng build` (no --configuration) uses angular.json's defaultConfiguration
# ("production") — see environment.ts, never environment.development.ts.
RUN npm run build

# ---- Runtime stage ----
# nginx-unprivileged: the official Nginx image's non-root variant — listens
# on 8080/8443 by default (no CAP_NET_BIND_SERVICE needed for a port <1024
# inside the container; the HOST-side port mapping in docker-compose.prod.yml
# is what actually publishes 80/443).
FROM nginxinc/nginx-unprivileged:1.27-alpine AS runtime

COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /src/dist/evidentia/browser /usr/share/nginx/html

EXPOSE 8080

# 127.0.0.1, not localhost: this image's /etc/hosts resolves "localhost"
# to ::1 (IPv6) before 127.0.0.1, but nginx.conf only listens on IPv4 —
# `wget http://localhost` fails with "connection refused" even though
# the server is up (confirmed while validating docker-compose.prod.yml).
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=5 \
    CMD wget -qO- http://127.0.0.1:8080/ || exit 1
