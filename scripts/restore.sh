#!/usr/bin/env bash
# Evidentia — Restore (System 17). Companion to scripts/backup.sh — see
# that script's header and docs/DEPLOYMENT.md's "Backup & Restore"
# section for why both PostgreSQL and MinIO must be restored together.
#
# Usage:
#   ./scripts/restore.sh <backup-dir>              # full restore
#   ./scripts/restore.sh <backup-dir> --verify-only # sanity-check only,
#                                                    # writes nothing
#
# --verify-only checks the backup directory's structural integrity
# (files present, the Postgres dump readable by pg_restore's own
# --list, a rough object count from the MinIO mirror) — it does NOT
# verify every evidence file's SHA-256 against PostgreSQL's stored hash;
# that full check is what running the application's own POST
# /documents/:id/verify against a RESTORED, running deployment gives you
# authoritatively (see docs/DEPLOYMENT.md), which this script — running
# with no application container involved — cannot do standalone.
#
# DESTRUCTIVE without --verify-only: a full restore overwrites the
# target database and bucket contents. This script always pauses for
# confirmation before doing so (skip with FORCE=1 for scripted/CI use —
# never as a casual habit).

set -euo pipefail

if [ $# -lt 1 ]; then
  echo "usage: $0 <backup-dir> [--verify-only]" >&2
  exit 1
fi

BACKUP_DIR="$(cd "$1" && pwd)"
VERIFY_ONLY="${2:-}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROJECT_NAME="${COMPOSE_PROJECT_NAME:-$(basename "$ROOT_DIR" | tr '[:upper:]' '[:lower:]')}"

POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-${PROJECT_NAME}-postgres-1}"
MINIO_CONTAINER="${MINIO_CONTAINER:-${PROJECT_NAME}-minio-1}"

: "${POSTGRES_USER:=evidentia}"
: "${POSTGRES_DB:=evidentia}"
: "${MINIO_ROOT_USER:=evidentia_minio}"
: "${MINIO_ROOT_PASSWORD:=changeme_example}"
: "${MINIO_BUCKET:=evidentia-documents}"

if [ ! -f "$BACKUP_DIR/postgres.dump" ] || [ ! -d "$BACKUP_DIR/minio" ]; then
  echo "error: $BACKUP_DIR does not look like a scripts/backup.sh output directory" >&2
  echo "       (expected postgres.dump and minio/ inside it)" >&2
  exit 1
fi

echo "==> Verifying $BACKUP_DIR/postgres.dump is a readable pg_dump archive"
docker exec -i "$POSTGRES_CONTAINER" pg_restore --list > /dev/null < "$BACKUP_DIR/postgres.dump"
echo "    OK"

MINIO_OBJECT_COUNT="$(find "$BACKUP_DIR/minio" -type f | wc -l | tr -d ' ')"
echo "==> MinIO mirror contains $MINIO_OBJECT_COUNT object(s)"
if [ -f "$BACKUP_DIR/manifest.txt" ]; then
  echo "==> Manifest:"
  sed 's/^/    /' "$BACKUP_DIR/manifest.txt"
fi

if [ "$VERIFY_ONLY" = "--verify-only" ]; then
  echo "==> --verify-only: nothing was written. Backup structure looks intact."
  exit 0
fi

echo
echo "!! This will OVERWRITE the '$POSTGRES_DB' database and the"
echo "!! '$MINIO_BUCKET' bucket's contents on containers '$POSTGRES_CONTAINER'"
echo "!! / '$MINIO_CONTAINER'. This cannot be undone."
if [ "${FORCE:-}" != "1" ]; then
  read -r -p "Type 'restore' to continue: " confirm
  if [ "$confirm" != "restore" ]; then
    echo "Aborted — nothing was changed."
    exit 1
  fi
fi

echo "==> Restoring PostgreSQL"
docker exec -i "$POSTGRES_CONTAINER" pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
  --clean --if-exists --no-owner < "$BACKUP_DIR/postgres.dump"

echo "==> Restoring MinIO bucket '$MINIO_BUCKET'"
docker run --rm \
  --network "container:$MINIO_CONTAINER" \
  -v "$BACKUP_DIR/minio:/backup:ro" \
  --entrypoint /bin/sh \
  minio/mc:latest -c "
    mc alias set dst http://localhost:9000 '$MINIO_ROOT_USER' '$MINIO_ROOT_PASSWORD' >/dev/null &&
    mc mb --ignore-existing dst/$MINIO_BUCKET >/dev/null &&
    mc mirror --quiet /backup dst/$MINIO_BUCKET
  "

echo "==> Restore complete."
echo "    Run the application's own document-verification flow (POST"
echo "    /documents/:id/verify) against a sample of restored documents"
echo "    to confirm hash integrity end to end — see"
echo "    docs/DEPLOYMENT.md's 'Restore' section."
