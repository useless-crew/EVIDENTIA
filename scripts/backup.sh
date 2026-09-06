#!/usr/bin/env bash
# Evidentia — Backup (System 17)
#
# Backs up BOTH halves of Evidentia's evidence model in one run — see
# docs/DEPLOYMENT.md's "Backup & Restore" section for why a
# PostgreSQL-only backup is not sufficient: case/document/audit METADATA,
# hashes, and user data live in PostgreSQL, but the raw evidence bytes
# live in MinIO. A backup missing either half cannot reconstruct working
# evidence: PostgreSQL alone has hashes with nothing to hash-check
# against; MinIO alone has files with no case/chain-of-custody context.
#
# Usage:
#   ./scripts/backup.sh [output-dir]
# Default output-dir: ./backups/<UTC timestamp>/
#
# What this does NOT do (see docs/DEPLOYMENT.md for how to add these for
# a real deployment — this script does not pretend to):
#   - Encrypt the backup at rest (pipe the output through gpg/age, or
#     rely on disk/volume-level encryption at the backup destination).
#   - Ship backups off-host (add your own `aws s3 sync`/`rsync`/etc.
#     after this script completes, or point BACKUP_DIR at an
#     already-mounted remote filesystem).
#   - Schedule itself. Run it from cron/systemd timer/your orchestrator's
#     job scheduler — see docs/DEPLOYMENT.md for an example crontab line.
#   - Enforce a retention policy. Old backup directories are left for you
#     to prune.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
OUT_DIR="${1:-$ROOT_DIR/backups/$TIMESTAMP}"
PROJECT_NAME="${COMPOSE_PROJECT_NAME:-$(basename "$ROOT_DIR" | tr '[:upper:]' '[:lower:]')}"

POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-${PROJECT_NAME}-postgres-1}"
MINIO_CONTAINER="${MINIO_CONTAINER:-${PROJECT_NAME}-minio-1}"

# Same placeholder-credential convention as .env.example — override via
# environment for a deployment using real values (never pass real
# secrets as script arguments, which land in shell history/process
# lists; export them in your shell or source a gitignored .env first).
: "${POSTGRES_USER:=evidentia}"
: "${POSTGRES_DB:=evidentia}"
: "${MINIO_ROOT_USER:=evidentia_minio}"
: "${MINIO_ROOT_PASSWORD:=changeme_example}"
: "${MINIO_BUCKET:=evidentia-documents}"

mkdir -p "$OUT_DIR"

echo "==> Backing up PostgreSQL ($POSTGRES_CONTAINER, database '$POSTGRES_DB') to $OUT_DIR/postgres.dump"
docker exec "$POSTGRES_CONTAINER" pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc \
  > "$OUT_DIR/postgres.dump"
echo "    $(du -h "$OUT_DIR/postgres.dump" | cut -f1)"

echo "==> Backing up MinIO bucket '$MINIO_BUCKET' ($MINIO_CONTAINER) to $OUT_DIR/minio/"
mkdir -p "$OUT_DIR/minio"
# Runs `mc mirror` inside a throwaway minio/mc container sharing the
# MinIO container's network namespace (so "localhost:9000" reaches it
# regardless of this deployment's actual network/service names) and
# bind-mounting the host output directory — no `mc` binary required on
# the host, and no MinIO port needs to be published to run this.
docker run --rm \
  --network "container:$MINIO_CONTAINER" \
  -v "$OUT_DIR/minio:/backup" \
  --entrypoint /bin/sh \
  minio/mc:latest -c "
    mc alias set src http://localhost:9000 '$MINIO_ROOT_USER' '$MINIO_ROOT_PASSWORD' >/dev/null &&
    mc mirror --quiet src/$MINIO_BUCKET /backup
  "
echo "    $(du -sh "$OUT_DIR/minio" | cut -f1)"

echo "==> Recording versions/metadata for this backup"
{
  echo "timestamp_utc=$TIMESTAMP"
  echo "postgres_db=$POSTGRES_DB"
  echo "minio_bucket=$MINIO_BUCKET"
  docker exec "$POSTGRES_CONTAINER" psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc \
    "SELECT 'schema_migration_version=' || version || ' dirty=' || dirty FROM schema_migrations ORDER BY version DESC LIMIT 1" 2>/dev/null || true
} > "$OUT_DIR/manifest.txt"

echo "==> Backup complete: $OUT_DIR"
echo "    Verify integrity before relying on this backup — see"
echo "    docs/DEPLOYMENT.md's 'Verifying a backup' section, or run:"
echo "      ./scripts/restore.sh $OUT_DIR --verify-only"
