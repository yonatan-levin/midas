#!/bin/sh
set -e

# Optional migration on startup for SQLite containers
if [ "${RUN_MIGRATIONS}" = "true" ] || [ "${RUN_MIGRATIONS}" = "1" ]; then
  echo "[entrypoint] Applying schema & migrations..."
  # Ensure data dir exists
  mkdir -p /app/data
  # Default path aligns with docker-compose env
  DB_PATH=${DATABASE_SQLITE_PATH:-/app/data/dcf.db}
  ./dcf-migrate -db "$DB_PATH"
  echo "[entrypoint] Migrations complete"
fi

exec "$@"


