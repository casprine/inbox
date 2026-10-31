#!/usr/bin/env bash
# Runs the real server locally for the iOS simulator: compose services, migrations, demo mail, a known device
# token, and inboxd on a fixed port.
#
#   scripts/dev-server.sh [run]   foreground (Ctrl-C stops it)          = make dev-server
#   scripts/dev-server.sh start   detached; waits until ready, prints base URL + token   = make dev-server-start
#   scripts/dev-server.sh stop    stops the detached server (compose keeps running)      = make dev-server-stop
#   scripts/dev-server.sh status  prints "running ..." or "stopped" (exit 0 / 1)
#
# Local development only: the token is public and grants full access to the demo data.
# Environment: PORT (default 18090), INGEST_PORT (default PORT+1), DEV_PASSWORD (login password, default dev-password-inbox-local), DEV_TOKEN (default below, >= 16 chars), DB_URL, S3_URL, LAN=1 (listen on 0.0.0.0 and print the
# Mac's LAN address as the base URL: anyone on the network can then use the public dev token, so only on a trusted network).
# Logs of the detached server: server/bin/dev-server.log. Never runs `docker compose down`.
set -euo pipefail
cd "$(dirname "$0")/.."

PORT="${PORT:-18090}"
# The ingest listener (only /ingest/v1/notify) gets the next port.
INGEST_PORT="${INGEST_PORT:-$((PORT + 1))}"
DEV_TOKEN="${DEV_TOKEN:-dev-token-inbox-local-0000}"
# Its own database: tests migrate into throw-away schemas of database "inbox" and break if that database
# already has tables in "public" (goose would see the existing version table and skip their migrations).
DB_URL="${DB_URL:-postgres://inbox:inbox@localhost:54329/inbox_dev?sslmode=disable}"
S3_URL="${S3_URL:-http://localhost:9009}"
# LAN=1 listens on every interface so a phone on the same Wi-Fi can reach it (the iOS device guide, docs/RUN_ON_DEVICE.md).
lan_ip() { local i ip; for i in en0 en1 en2 en3 en4 en5 en6; do ip="$(ipconfig getifaddr "$i" 2>/dev/null || true)"; [ -n "$ip" ] && { echo "$ip"; return; }; done; }
BIND="127.0.0.1"; BASE="http://127.0.0.1:$PORT"
if [ "${LAN:-0}" = "1" ]; then
  LAN_IP="$(lan_ip)"; [ -n "$LAN_IP" ] || { echo "LAN=1: no Wi-Fi/Ethernet address found" >&2; exit 1; }
  BIND="0.0.0.0"; BASE="http://$LAN_IP:$PORT"
fi
PIDFILE="server/bin/dev-server.pid"
LOGFILE="server/bin/dev-server.log"

ready() { # HTTP GET /readyz without curl
  (exec 3<>"/dev/tcp/${LAN_IP:-127.0.0.1}/$PORT" && printf 'GET /readyz HTTP/1.0\r\n\r\n' >&3 && head -n1 <&3 | grep -q ' 200 ') 2>/dev/null
}

running() { [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; }

banner() {
  cat <<MSG

  Inbox dev server
    base URL : $BASE
    token    : $DEV_TOKEN      (send as: Authorization: Bearer <token>)
    password : ${DEV_PASSWORD:-dev-password-inbox-local}      (POST /v1/auth/login)
    simulator: launch the app with environment INBOX_API_BASE_URL=$BASE
               and INBOX_DEV_TOKEN=$DEV_TOKEN   (see docs/DEV_SERVER.md)

MSG
}

prepare() {
  make up >/dev/null
  docker compose -f deploy/docker-compose.yml exec -T postgres psql -U inbox -d inbox -tc "SELECT 1 FROM pg_database WHERE datname='inbox_dev'" | grep -q 1 \
    || docker compose -f deploy/docker-compose.yml exec -T postgres psql -U inbox -d inbox -c "CREATE DATABASE inbox_dev" >/dev/null
  mkdir -p server/bin
  (cd server && go build -o bin/inboxd ./cmd/inboxd)
  export DATABASE_URL="$DB_URL" HTTP_ADDR="$BIND:$PORT" PUBLIC_BASE_URL="$BASE"
  export S3_ENDPOINT="$S3_URL" S3_BUCKET=inbox-mail S3_ACCESS_KEY=minio S3_SECRET_KEY=minio12345 S3_REGION=auto
  export INGEST_SECRET=dev-ingest-secret INGEST_ADDR="$BIND:$INGEST_PORT"
  # Sign-in password for POST /v1/auth/login (public, dev only; the app normally uses the fixed dev token below).
  export OWNER_PASSWORD="${DEV_PASSWORD:-dev-password-inbox-local}"
  export TOKEN_ENC_KEY="$(printf '0123456789abcdef0123456789abcdef' | base64)"
  server/bin/inboxd migrate >/dev/null 2>&1
  server/bin/inboxd admin seed-demo
  server/bin/inboxd admin dev-token "$DEV_TOKEN"
}

case "${1:-run}" in
  run)
    prepare
    banner
    exec server/bin/inboxd serve
    ;;
  start)
    if running && ready; then banner; echo "  (already running, pid $(cat "$PIDFILE"))"; exit 0; fi
    if ready; then echo "port $PORT already answers and it is not our server; set PORT=..." >&2; exit 1; fi
    prepare
    nohup server/bin/inboxd serve >"$LOGFILE" 2>&1 </dev/null &
    echo $! >"$PIDFILE"
    disown 2>/dev/null || true
    for _ in $(seq 1 100); do
      if ready; then banner; echo "  (detached, pid $(cat "$PIDFILE"), log $LOGFILE)"; exit 0; fi
      if ! running; then echo "server exited; see $LOGFILE" >&2; tail -n 20 "$LOGFILE" >&2; exit 1; fi
      sleep 0.2
    done
    echo "server did not become ready; see $LOGFILE" >&2
    exit 1
    ;;
  stop)
    if running; then
      pid="$(cat "$PIDFILE")"
      kill "$pid" 2>/dev/null || true
      for _ in $(seq 1 50); do kill -0 "$pid" 2>/dev/null || break; sleep 0.2; done
      kill -9 "$pid" 2>/dev/null || true
      echo "stopped dev server (pid $pid)"
    else
      echo "dev server not running"
    fi
    rm -f "$PIDFILE"
    ;;
  status)
    if running && ready; then echo "running pid $(cat "$PIDFILE") $BASE"; else echo "stopped"; exit 1; fi
    ;;
  *)
    echo "usage: $0 [run|start|stop|status]" >&2
    exit 2
    ;;
esac
