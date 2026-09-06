#!/usr/bin/env bash
# Generates a self-signed TLS certificate for the reverse proxy — for
# LOCAL "production-like" testing and SIH demo purposes only. A browser
# will show a certificate-trust warning; that is expected and correct
# for a self-signed cert, not a bug to silence.
#
# For a REAL production deployment with a real domain, replace the files
# this script writes with a certificate from your CA/ACME provider
# (e.g. Let's Encrypt via certbot, or one issued by your organization) —
# see docs/DEPLOYMENT.md's "TLS / HTTPS" section.
#
# Usage: ops/reverse-proxy/generate-dev-certs.sh [output-dir]
# Default output-dir: backend/certs (already gitignored — see .gitignore's
# *.pem/*.key/*.crt rules).

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT_DIR="${1:-$ROOT_DIR/backend/certs}"

mkdir -p "$OUT_DIR"

if [ -f "$OUT_DIR/fullchain.pem" ] && [ -f "$OUT_DIR/privkey.pem" ]; then
  echo "Certificates already exist at $OUT_DIR — remove them first if you want to regenerate." >&2
  exit 0
fi

echo "==> Generating a self-signed certificate for local/demo use in $OUT_DIR"

openssl req -x509 -nodes -newkey rsa:2048 \
  -keyout "$OUT_DIR/privkey.pem" \
  -out "$OUT_DIR/fullchain.pem" \
  -days 365 \
  -subj "/C=IN/O=Evidentia Demo/CN=localhost" \
  -addext "subjectAltName=DNS:localhost,IP:127.0.0.1"

# 644, not the usual 600: the reverse-proxy container runs nginx as a
# non-root, non-host UID (nginxinc/nginx-unprivileged), and this file is
# bind-mounted read-only from the host — a stricter host-side mode would
# make it unreadable inside the container ("Permission denied" at nginx
# startup, confirmed while validating docker-compose.prod.yml). This is
# an acceptable trade-off for a throwaway self-signed dev/demo key; see
# backend/certs/README.md's note for what a REAL production key needs
# instead (readable by whatever UID your actual reverse-proxy container
# runs as — not necessarily 644).
chmod 644 "$OUT_DIR/privkey.pem"

echo "==> Done. This certificate is self-signed and NOT trusted by browsers by"
echo "    default — that warning is expected for local/demo use. Never use"
echo "    these files for a real production deployment."
