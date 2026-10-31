#!/usr/bin/env bash
# End-to-end check of the server: starts Postgres + object storage (docker compose), builds inboxd, then
# plays the Cloudflare Worker, Google (OAuth + Gmail) and Cloudflare's API with local fakes against a real
# inboxd process and asserts what the app would see through the HTTP API.
#
#   scripts/e2e.sh                 # run (starts the compose services if needed and leaves them running)
#   E2E_WORKER=sim scripts/e2e.sh  # use the Go simulation of the Worker instead of the real one (no Node needed)
#
# The Worker is the real edge/src/index.ts run by Node 22.6+ (edge/e2e/harness.mjs). Takes about a minute (it waits out the real 8 s archive-undo window and a few job cycles).
# Uses a throw-away database; touches nothing outside this machine. curl is not needed.
set -euo pipefail
cd "$(dirname "$0")/.."

DB_URL="${DB_URL:-postgres://inbox:inbox@localhost:54329/inbox?sslmode=disable}"
S3_URL="${S3_URL:-http://localhost:9009}"

echo "== starting services"
make up >/dev/null

echo "== building"
mkdir -p server/bin
(cd server && go build -o bin/inboxd ./cmd/inboxd && go build -o bin/e2e ./cmd/e2e)

echo "== running"
E2E_DB_ADMIN_URL="$DB_URL" E2E_S3_ENDPOINT="$S3_URL" E2E_INBOXD="$PWD/server/bin/inboxd" server/bin/e2e
