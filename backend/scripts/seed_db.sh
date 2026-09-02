#!/usr/bin/env bash
set -euo pipefail

# Evidentia — Development Reference-Data Seed
#
# Applies backend/db/seed/*.sql (roles, permissions, role_permissions) via
# psql. Idempotent — safe to run more than once (see the seed files'
# ON CONFLICT DO NOTHING clauses).
#
# Connects with the same DATABASE_MIGRATOR_USER/PASSWORD as cmd/migrate,
# not the runtime evidentia_app credentials: role_permissions is
# read-only for the app role (see the migration), so seeding it requires
# the privileged migrator role.
#
# Reads DATABASE_HOST/PORT/NAME/SSLMODE/DATABASE_MIGRATOR_USER/
# DATABASE_MIGRATOR_PASSWORD from the environment or backend/.env.

cd "$(dirname "${BASH_SOURCE[0]}")/.."

if [ -f .env ]; then
    set -a
    # shellcheck disable=SC1091
    source .env
    set +a
fi

: "${DATABASE_HOST:=localhost}"
: "${DATABASE_PORT:=5432}"
: "${DATABASE_NAME:?DATABASE_NAME must be set}"
: "${DATABASE_MIGRATOR_USER:?DATABASE_MIGRATOR_USER must be set}"
: "${DATABASE_MIGRATOR_PASSWORD:?DATABASE_MIGRATOR_PASSWORD must be set}"
: "${DATABASE_SSLMODE:=disable}"

export PGPASSWORD="${DATABASE_MIGRATOR_PASSWORD}"
export PGSSLMODE="${DATABASE_SSLMODE}"

for seed_file in db/seed/*.sql; do
    echo "seed_db: applying ${seed_file}"
    psql \
        -h "${DATABASE_HOST}" \
        -p "${DATABASE_PORT}" \
        -U "${DATABASE_MIGRATOR_USER}" \
        -d "${DATABASE_NAME}" \
        -v "ON_ERROR_STOP=1" \
        -f "${seed_file}"
done

echo "seed_db: complete"
