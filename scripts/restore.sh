#!/usr/bin/env bash
# Restore a dump made by scripts/backup.sh. Two modes:
#
#   scripts/restore.sh DUMP --into NEWDB     restore into a NEW database beside the live one (a rehearsal; refuses
#                                            if NEWDB exists). The server keeps running and is not touched.
#   scripts/restore.sh DUMP --replace --yes  replace the live database `inbox` with the dump: stops the server,
#                                            drops and recreates the database, restores, starts the server again.
#                                            Without --yes it only prints what it would do.
#
# Settings: COMPOSE_FILE (default deploy/docker-compose.prod.yml), COMPOSE_PROJECT (default inbox-prod), ENV_FILE
# (default deploy/.env). Procedure and what is lost with the database: docs/RUNBOOK.md ("Restore").
set -euo pipefail
cd "$(dirname "$0")/.."

COMPOSE_FILE="${COMPOSE_FILE:-deploy/docker-compose.prod.yml}"
COMPOSE_PROJECT="${COMPOSE_PROJECT:-inbox-prod}"
ENV_FILE="${ENV_FILE:-deploy/.env}"
dc() { docker compose -p "$COMPOSE_PROJECT" -f "$COMPOSE_FILE" --env-file "$ENV_FILE" "$@"; }
usage() { sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2; }

dump="${1:-}"; [ -n "$dump" ] && [ -f "$dump" ] || usage
shift
mode="" target="" yes=""
while [ $# -gt 0 ]; do
  case "$1" in
    --into) mode=into; target="${2:-}"; shift 2 || usage;;
    --replace) mode=replace; shift;;
    --yes) yes=1; shift;;
    *) usage;;
  esac
done
[ -n "$mode" ] || usage
psql_() { dc exec -T postgres psql -U inbox -d postgres -v ON_ERROR_STOP=1 -qAt "$@"; }

# The dump must be readable before anything is dropped.
dc exec -T postgres pg_restore --list < "$dump" > /dev/null || { echo "restore: $dump is not a readable dump" >&2; exit 1; }

if [ "$mode" = into ]; then
  [[ "$target" =~ ^[a-z_][a-z0-9_]{0,40}$ ]] || { echo "restore: --into needs a simple lower-case database name" >&2; exit 1; }
  [ "$target" != inbox ] && [ "$target" != postgres ] || { echo "restore: use --replace to restore over the live database" >&2; exit 1; }
  [ -z "$(psql_ -c "SELECT 1 FROM pg_database WHERE datname='$target'")" ] || { echo "restore: database $target already exists" >&2; exit 1; }
  psql_ -c "CREATE DATABASE $target OWNER inbox"
  dc exec -T postgres pg_restore -U inbox -d "$target" --no-owner --exit-on-error < "$dump"
  echo "restore: restored into database $target (the live database was not touched)"
  exit 0
fi

if [ -z "$yes" ]; then
  echo "restore: would stop the server, drop and recreate database inbox, restore $dump, start the server."
  echo "restore: take a fresh backup first (scripts/backup.sh); re-run with --yes to proceed."
  exit 0
fi
echo "restore: stopping the server"
dc stop inbox
psql_ -c "DROP DATABASE IF EXISTS inbox WITH (FORCE)" -c "CREATE DATABASE inbox OWNER inbox"
dc exec -T postgres pg_restore -U inbox -d inbox --no-owner --exit-on-error < "$dump"
echo "restore: starting the server (it applies any newer migrations itself)"
dc up -d --wait inbox
echo "restore: done. Check: docker compose -p $COMPOSE_PROJECT ps ; the app signs in and shows the mail."
