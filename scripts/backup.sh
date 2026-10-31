#!/usr/bin/env bash
# Dump the production Postgres (custom format, compressed), verify the dump is readable, keep the newest N,
# optionally copy it off-site. Safe to run while the server is running (pg_dump takes a consistent snapshot).
#
#   scripts/backup.sh                 # uses deploy/.env, writes to $BACKUP_DIR (default ~/inbox-backups)
#   scripts/backup.sh /some/dir
#
# Settings (environment or deploy/.env): BACKUP_DIR, BACKUP_KEEP (default 14),
#   BACKUP_S3_ENDPOINT / BACKUP_S3_BUCKET / BACKUP_S3_ACCESS_KEY / BACKUP_S3_SECRET_KEY (all four, optional upload; needs `aws`),
#   COMPOSE_FILE (default deploy/docker-compose.prod.yml), COMPOSE_PROJECT (default inbox-prod), ENV_FILE (default deploy/.env).
# What a dump contains and what it does not: docs/RUNBOOK.md ("Backups").
set -euo pipefail
cd "$(dirname "$0")/.."

COMPOSE_FILE="${COMPOSE_FILE:-deploy/docker-compose.prod.yml}"
COMPOSE_PROJECT="${COMPOSE_PROJECT:-inbox-prod}"
ENV_FILE="${ENV_FILE:-deploy/.env}"
[ -f "$ENV_FILE" ] || { echo "backup: $ENV_FILE not found" >&2; exit 1; }
dc() { docker compose -p "$COMPOSE_PROJECT" -f "$COMPOSE_FILE" --env-file "$ENV_FILE" "$@"; }
# Read one KEY=value from the env file without sourcing it (values may contain $ or ~).
envval() { local v; v="$(grep -E "^$1=" "$ENV_FILE" | tail -n1 | cut -d= -f2-)" || true; printf '%s' "${v%\"}" | sed 's/^"//'; }
setting() { local cur="${!1:-}"; if [ -n "$cur" ]; then printf '%s' "$cur"; else envval "$1"; fi; }

dir="${1:-$(setting BACKUP_DIR)}"; dir="${dir:-$HOME/inbox-backups}"; dir="${dir/#\~/$HOME}"
keep="$(setting BACKUP_KEEP)"; keep="${keep:-14}"
case "$keep" in ''|*[!0-9]*) echo "backup: BACKUP_KEEP must be a number" >&2; exit 1;; esac
[ "$keep" -ge 1 ] || { echo "backup: BACKUP_KEEP must be at least 1" >&2; exit 1; }

mkdir -p "$dir"; chmod 700 "$dir"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
final="$dir/inbox-$stamp.dump"
tmp="$final.partial"
trap 'rm -f "$tmp"' EXIT

dc exec -T postgres pg_dump -U inbox -d inbox --format=custom --no-owner > "$tmp"
# A dump that cannot be listed is not a backup.
dc exec -T postgres pg_restore --list < "$tmp" > /dev/null
[ -s "$tmp" ] || { echo "backup: empty dump" >&2; exit 1; }
chmod 600 "$tmp"; mv "$tmp" "$final"; trap - EXIT
echo "backup: wrote $final ($(wc -c < "$final" | tr -d ' ') bytes)"

# Rotation: keep the newest $keep dumps (names sort by timestamp).
ls -1 "$dir"/inbox-*.dump 2>/dev/null | sort | awk -v k="$keep" '{a[NR]=$0} END{for(i=1;i<=NR-k;i++) print a[i]}' | while read -r old; do
  rm -f -- "$old"; echo "backup: removed old $old"
done

# Optional off-site copy, only when all four BACKUP_S3_* values are set.
ep="$(setting BACKUP_S3_ENDPOINT)"; bk="$(setting BACKUP_S3_BUCKET)"; ak="$(setting BACKUP_S3_ACCESS_KEY)"; sk="$(setting BACKUP_S3_SECRET_KEY)"
if [ -n "$ep$bk$ak$sk" ]; then
  if [ -z "$ep" ] || [ -z "$bk" ] || [ -z "$ak" ] || [ -z "$sk" ]; then
    echo "backup: BACKUP_S3_* is partly set; set all four or none (local copy kept)" >&2; exit 1
  fi
  command -v aws > /dev/null || { echo "backup: the aws CLI is needed for the off-site copy (brew install awscli)" >&2; exit 1; }
  AWS_ACCESS_KEY_ID="$ak" AWS_SECRET_ACCESS_KEY="$sk" AWS_DEFAULT_REGION=auto \
    aws s3 cp --only-show-errors --endpoint-url "$ep" "$final" "s3://$bk/$(basename "$final")"
  echo "backup: uploaded to s3://$bk/$(basename "$final")"
fi
