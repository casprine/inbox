# Inbox

See `ENGINEERING_PLAN.md`, `MILESTONES.md`, `DECISIONS.md`.

**API contract:** `api/openapi.yaml` is the single source of truth; any task that adds or changes a route must update it in the same change (`server/internal/contract` fails if routes and spec diverge).

## Run locally

```
make up                                   # Postgres :54329, MinIO :9009 (bucket inbox-mail)
export DATABASE_URL='postgres://inbox:inbox@localhost:54329/inbox?sslmode=disable'
cd server
go run ./cmd/inboxd migrate
go run ./cmd/inboxd serve                 # :8080  /healthz /readyz
cd .. && make test                        # unit tests; `make test-integration` adds infra
make down
```

## Test end to end locally

`make dev-server-start`, then `make send-test-mail` (options `TO FROM SUBJECT BODY EML COUNT SAMPLE=invoice|reply|unrouted|newsletter`): the mail goes through the real Worker harness into the local server and the command prints where it landed (business, routed/unrouted, rule). Pull to refresh in the app to see it. Stop with `make dev-server-stop`. Details: `docs/DEV_SERVER.md`.

## Deploy and operate

- `docs/DEPLOY.md`: every step you do once (Cloudflare, Tailscale, Google OAuth, the Mac mini Docker setup), settings table, environments.
- `docs/RUNBOOK.md`: what each Health row means and how to fix it, routine operations.
- `make docker-build` builds the server image (`deploy/Dockerfile`); `make ci` runs what CI runs for the server and edge worker.
- `make e2e` (`scripts/e2e.sh`) runs a real `inboxd` against fake Worker/Google/Cloudflare and checks what the app would see: ingest, threading, rules, search, Gmail connect and write-back, failed-mail retry, older-mail import, health, Alfather over MCP, delete-everything (about a minute).
- `make dev-server` runs the real server with demo data and a known token for the iOS simulator: see `docs/DEV_SERVER.md`.
