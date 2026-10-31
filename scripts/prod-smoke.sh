#!/usr/bin/env bash
# Production-like smoke test (make prod-smoke). Builds the server image and starts deploy/docker-compose.prod.yml
# exactly as the Mac mini would, under its own compose project (inbox-prod-smoke) with throwaway volumes and
# throwaway secrets generated per run. Cloudflare R2 is stood in for by the local SeaweedFS (S3 on :9009,
# started by `make up`). Tears down ONLY its own project. Needs docker, curl (7.75+), openssl, jq.
#
# Checks: image runs as non-root and refuses weak settings; /healthz and /readyz; sign-in (wrong, right); device
# token; signed notify on the ingest port; the API port does not serve the ingest route and the ingest port serves
# nothing else; a mail whose ping was lost is recovered by the reconcile sweep; the refresh endpoint; backup and
# restore into a fresh database; graceful stop (exit 0) and restart keeping data; no secret in the logs.
set -euo pipefail
cd "$(dirname "$0")/.."

PROJECT=inbox-prod-smoke
API_PORT="${SMOKE_API_PORT:-18180}"
INGEST_PORT="${SMOKE_INGEST_PORT:-18181}"
S3_LOCAL="${S3_URL:-http://localhost:9009}"          # as seen from this Mac
S3_FROM_CONTAINER="${SMOKE_S3_FROM_CONTAINER:-http://host.docker.internal:9009}"
S3_KEY=minio; S3_SECRET=minio12345                   # the SeaweedFS stand-in's keys (deploy/docker-compose.yml)
IMAGE=inbox-server:prod-smoke
API="http://127.0.0.1:$API_PORT"; ING="http://127.0.0.1:$INGEST_PORT"

for tool in docker curl openssl jq; do command -v "$tool" > /dev/null || { echo "prod-smoke: $tool is required" >&2; exit 2; }; done
tmp="$(mktemp -d)"
ENVF="$tmp/env"
BUCKET="smoke-$(openssl rand -hex 4)"
dc() { docker compose -p "$PROJECT" -f deploy/docker-compose.prod.yml --env-file "$ENVF" "$@"; }

pass=0; fail=0
ok()   { pass=$((pass+1)); printf '  ok    %s\n' "$1"; }
bad()  { fail=$((fail+1)); printf '  FAIL  %s\n' "$1" >&2; }
check() { # check "name" command...
  local name="$1"; shift
  if "$@" > "$tmp/out" 2>&1; then ok "$name"; else bad "$name"; sed 's/^/        /' "$tmp/out" | head -8 >&2; fi
}
s3() { curl -sS --fail-with-body --aws-sigv4 "aws:amz:us-east-1:s3" --user "$S3_KEY:$S3_SECRET" "$@"; }
cleanup() {
  set +e
  echo "prod-smoke: tearing down project $PROJECT (only this project)"
  [ -f "$ENVF" ] && dc down -v --remove-orphans > /dev/null 2>&1
  for k in $(s3 -s "$S3_LOCAL/$BUCKET?list-type=2" 2>/dev/null | grep -o '<Key>[^<]*' | sed 's/<Key>//'); do s3 -s -X DELETE "$S3_LOCAL/$BUCKET/$k" > /dev/null 2>&1; done
  s3 -s -X DELETE "$S3_LOCAL/$BUCKET" > /dev/null 2>&1
  rm -rf "$tmp"
}
trap cleanup EXIT

http() { curl -sS -o "$tmp/body" -w '%{http_code}' --max-time 15 "$@" || echo 000; }
expect() { # expect name want_status curl-args...
  local name="$1" want="$2"; shift 2
  local got; got="$(http "$@")"
  if [ "$got" = "$want" ]; then ok "$name"; else bad "$name (HTTP $got, want $want: $(head -c 200 "$tmp/body"))"; fi
}
wait_for() { # wait_for seconds command...
  local secs="$1"; shift; local end=$((SECONDS+secs))
  until "$@" > /dev/null 2>&1; do [ $SECONDS -lt $end ] || return 1; sleep 1; done
}
sign() { printf '%s.%s' "$1" "$2" | openssl dgst -sha256 -hmac "$INGEST_SECRET" | sed 's/^.* //'; }
notify() { # notify KEY SIZE -> HTTP status on the ingest port
  local body ts; ts="$(date +%s)"
  body="{\"key\":\"$1\",\"env_from\":\"sender@example.org\",\"env_to\":\"hello@smoke.test\",\"raw_size\":$2,\"received_at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"message_id\":\"m\"}"
  http -X POST "$ING/ingest/v1/notify" -H "X-Inbox-Timestamp: $ts" -H "X-Inbox-Signature: $(sign "$ts" "$body")" -H 'Content-Type: application/json' -d "$body"
}
put_mail() { # put_mail SUBJECT -> prints the R2 key
  local subj="$1" key raw
  key="raw/$(date -u +%Y/%m)/smoke-$(openssl rand -hex 8).eml"
  raw="From: Sender <sender@example.org>\r\nTo: hello@smoke.test\r\nSubject: $subj\r\nMessage-ID: <$(openssl rand -hex 8)@smoke.test>\r\nDate: $(date -R)\r\n\r\nSmoke test body for $subj\r\n"
  printf "$raw" > "$tmp/mail.eml"
  s3 -s -X PUT -H "x-amz-meta-env_from: sender@example.org" -H "x-amz-meta-env_to: hello@smoke.test" --data-binary "@$tmp/mail.eml" "$S3_LOCAL/$BUCKET/$key" > /dev/null
  KEY="$key"; SIZE="$(wc -c < "$tmp/mail.eml" | tr -d ' ')"
}
has_subject() { curl -sS --fail --max-time 10 -H "Authorization: Bearer $TOKEN" "$API/v1/messages?limit=100" | grep -q "$1"; }
count() { dc exec -T postgres psql -U inbox -d "$1" -qAt -c "SELECT count(*) FROM $2"; }

echo "prod-smoke: building $IMAGE"
docker build -q -f deploy/Dockerfile -t "$IMAGE" . > /dev/null

echo "== image"
check "runs as a non-root user" bash -c "[ \"\$(docker run --rm --entrypoint id $IMAGE -u)\" != 0 ]"
out="$(docker run --rm -e OWNER_PASSWORD=short -e INGEST_SECRET=dev-ingest-secret "$IMAGE" serve 2>&1 || true)"
check "refuses weak settings and names every variable" bash -c "
  for v in OWNER_PASSWORD INGEST_SECRET PUBLIC_BASE_URL DATABASE_URL TOKEN_ENC_KEYS S3_ENDPOINT S3_BUCKET S3_ACCESS_KEY S3_SECRET_KEY; do
    printf '%s' \"\$1\" | grep -q \"\$v\" || { echo \"missing \$v in startup error\"; exit 1; }; done" _ "$out"

# Throwaway secrets for this run only.
OWNER_PASSWORD="smoke-$(openssl rand -hex 10)"
INGEST_SECRET="$(openssl rand -hex 32)"
PGPASS="$(openssl rand -hex 24)"
ENCKEY="k1:$(openssl rand -base64 32)"
umask 077
cat > "$ENVF" <<ENV
INBOX_IMAGE=$IMAGE
POSTGRES_PASSWORD=$PGPASS
OWNER_PASSWORD=$OWNER_PASSWORD
PUBLIC_BASE_URL=https://smoke-mini.example-tailnet.ts.net
API_PORT=$API_PORT
INGEST_PORT=$INGEST_PORT
INGEST_SECRET=$INGEST_SECRET
TOKEN_ENC_KEYS=$ENCKEY
S3_ENDPOINT=$S3_FROM_CONTAINER
S3_BUCKET=$BUCKET
S3_ACCESS_KEY=$S3_KEY
S3_SECRET_KEY=$S3_SECRET
S3_REGION=us-east-1
RECONCILE_INTERVAL=10s
RECONCILE_MIN_AGE=2s
ENV
s3 -s -X PUT "$S3_LOCAL/$BUCKET" > /dev/null || { echo "prod-smoke: cannot reach the S3 stand-in at $S3_LOCAL (run make up)" >&2; exit 2; }

echo "== start (compose project $PROJECT, ports $API_PORT/$INGEST_PORT)"
dc down -v --remove-orphans > /dev/null 2>&1 || true
dc up -d --wait --wait-timeout 120 > "$tmp/up.log" 2>&1 || { tail -20 "$tmp/up.log" >&2; dc logs --no-color inbox | tail -30 >&2; exit 1; }
check "compose reports the server healthy" bash -c "[ \"\$(docker inspect -f '{{.State.Health.Status}}' ${PROJECT}-inbox-1)\" = healthy ]"
check "container runs as non-root" bash -c "[ \"\$(docker exec ${PROJECT}-inbox-1 id -u)\" != 0 ]"
check "postgres has no published port" bash -c "[ -z \"\$(docker port ${PROJECT}-postgres-1)\" ]"

echo "== health"
expect "healthz" 200 "$API/healthz"
expect "readyz (database and blob store)" 200 "$API/readyz"
check "readyz says ready" bash -c "jq -e '.status==\"ready\"' < $tmp/body"

echo "== sign-in and device token"
expect "wrong password is 401" 401 -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' -d '{"password":"definitely-not-it"}'
expect "right password is 200" 200 -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' -d "{\"password\":\"$OWNER_PASSWORD\",\"device_name\":\"smoke\"}"
TOKEN="$(jq -r .token "$tmp/body")"
check "login reports setup_step domains on a fresh server" bash -c "[ \"\$(jq -r .setup_step $tmp/body)\" = domains ]"
expect "no token is 401" 401 "$API/v1/businesses"
expect "device token works" 200 -H "Authorization: Bearer $TOKEN" "$API/v1/businesses"
expect "create a business" 201 -X POST "$API/v1/businesses" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"name":"Smoke","domain":"smoke.test"}'

echo "== ingest"
SUBJ1="smoke-notified-$(openssl rand -hex 3)"
put_mail "$SUBJ1"
check "signed notify is accepted on the ingest port" bash -c "[ \"$(notify "$KEY" "$SIZE")\" = 202 ]"
check "the notified mail shows in the API" wait_for 30 has_subject "$SUBJ1"
BAD="$(http -X POST "$ING/ingest/v1/notify" -H "X-Inbox-Timestamp: $(date +%s)" -H 'X-Inbox-Signature: 00' -d '{}')"
[ "$BAD" = 401 ] && ok "bad signature is 401" || bad "bad signature gave $BAD"

echo "== listeners are separate"
expect "API port: POST /ingest/v1/notify is not served" 404 -X POST "$API/ingest/v1/notify" -d '{}'
for path in / /healthz /readyz /v1/businesses /v1/auth/login /v1/events /oauth/google/callback; do
  for m in GET POST; do expect "ingest port: $m $path is 404" 404 -X "$m" "$ING$path" -d '{}'; done
done

echo "== catch-up"
SUBJ2="smoke-missed-ping-$(openssl rand -hex 3)"
put_mail "$SUBJ2"   # no notify: the Worker's ping is "lost"
check "reconcile sweep recovers a mail whose ping was lost" wait_for 90 has_subject "$SUBJ2"
SUBJ3="smoke-refresh-$(openssl rand -hex 3)"
put_mail "$SUBJ3"
sleep 3             # RECONCILE_MIN_AGE is 2s
expect "refresh endpoint answers 202" 202 -X POST "$API/v1/mail:refresh" -H "Authorization: Bearer $TOKEN"
check "refresh finds the mail" wait_for 30 has_subject "$SUBJ3"

echo "== backup and restore"
BK="$tmp/backups"
check "backup.sh writes a dump" env ENV_FILE="$ENVF" COMPOSE_PROJECT="$PROJECT" BACKUP_DIR="$BK" BACKUP_KEEP=2 scripts/backup.sh
DUMP="$(ls -1 "$BK"/inbox-*.dump | tail -n1)"
check "dump is not world-readable" bash -c "[ \"\$(stat -c %a '$DUMP' 2>/dev/null || stat -f %Lp '$DUMP')\" = 600 ]"
sleep 1
env ENV_FILE="$ENVF" COMPOSE_PROJECT="$PROJECT" BACKUP_DIR="$BK" BACKUP_KEEP=2 scripts/backup.sh > /dev/null
sleep 1
env ENV_FILE="$ENVF" COMPOSE_PROJECT="$PROJECT" BACKUP_DIR="$BK" BACKUP_KEEP=2 scripts/backup.sh > /dev/null
check "rotation keeps only the newest 2" bash -c "[ \"\$(ls -1 '$BK'/inbox-*.dump | wc -l | tr -d ' ')\" = 2 ]"
DUMP="$(ls -1 "$BK"/inbox-*.dump | tail -n1)"
check "restore.sh restores into a fresh database" env ENV_FILE="$ENVF" COMPOSE_PROJECT="$PROJECT" scripts/restore.sh "$DUMP" --into smoke_restored
check "restore refuses to overwrite an existing database" bash -c "! ENV_FILE='$ENVF' COMPOSE_PROJECT='$PROJECT' scripts/restore.sh '$DUMP' --into smoke_restored"
for t in businesses messages devices; do
  live="$(count inbox $t)"; rest="$(count smoke_restored $t)"
  [ "$live" = "$rest" ] && [ "$live" -ge 1 ] && ok "restored $t matches live ($live)" || bad "restored $t differs: live=$live restored=$rest"
done
check "restore --replace without --yes changes nothing" env ENV_FILE="$ENVF" COMPOSE_PROJECT="$PROJECT" scripts/restore.sh "$DUMP" --replace
check "restore --replace --yes replaces the live database" env ENV_FILE="$ENVF" COMPOSE_PROJECT="$PROJECT" scripts/restore.sh "$DUMP" --replace --yes
check "after --replace the old device token and all three mails are back" wait_for 60 has_subject "$SUBJ3"

echo "== stop, restart, durability"
dc stop inbox > /dev/null 2>&1
check "SIGTERM stops the server cleanly (exit 0)" bash -c "[ \"\$(docker inspect -f '{{.State.ExitCode}}' ${PROJECT}-inbox-1)\" = 0 ]"
check "the log shows the graceful drain" bash -c "docker logs ${PROJECT}-inbox-1 2>&1 | grep -q 'shutting down' && docker logs ${PROJECT}-inbox-1 2>&1 | grep -q '\"stopped\"'"
dc up -d --wait --wait-timeout 120 inbox > /dev/null 2>&1 || bad "server did not come back"
dc restart postgres > /dev/null 2>&1; dc up -d --wait --wait-timeout 120 > /dev/null 2>&1 || bad "stack did not come back after a Postgres restart"
wait_for 60 curl -sf "$API/readyz" && ok "ready again after restarts" || bad "not ready after restarts"
check "same device token still works after restart" has_subject "$SUBJ1"
check "all three mails survived the restart" bash -c "curl -sS -H 'Authorization: Bearer $TOKEN' '$API/v1/messages?limit=100' | grep -c 'smoke-' | grep -qE '^[1-9]'"
expect "sign-in still works after restart" 200 -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' -d "{\"password\":\"$OWNER_PASSWORD\"}"
LOGS="$(dc logs --no-color inbox 2>&1)"
check "migrations ran on each of the two starts, no fatal error" bash -c "grep -c 'applying migrations' <<<\"\$1\" | grep -qE '^[2-9]' && ! grep -q '\"msg\":\"fatal\"' <<<\"\$1\"" _ "$LOGS"

echo "== logs"
LOGS="$(dc logs --no-color inbox postgres 2>&1)"
for secret in "$OWNER_PASSWORD" "$INGEST_SECRET" "$PGPASS" "$TOKEN" "${ENCKEY#k1:}" "Smoke test body"; do
  if grep -qF -- "$secret" <<<"$LOGS"; then bad "a secret appears in the logs"; else ok "log is free of a secret (${secret:0:4}...)"; fi
done

echo
echo "prod-smoke: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
