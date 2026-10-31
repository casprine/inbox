# State of the backend

What exists, what was tested and how, what is assumed or stubbed, and what only you can do. Written at the end of the backend queue
(T18-T29 server half plus the T30 server audit); updated after Phase 2 backend work (B-1 to B-7), the 2026-10-31 hosting change and round C (production hardening, backups, `make prod-smoke`) (D-012 password sign-in, D-018 to D-020: Mac mini, Docker, Tailscale, R2 catch-up). The iOS app has its own status. Decisions behind everything here: `DECISIONS.md` (Q-T*, D-*).

## At a glance

| | |
|---|---|
| API | 61 operations on 52 paths in `api/openapi.yaml` (`/v1/auth/login` replaces `/v1/auth/apple`; new `/v1/mail:refresh`); a Go test fails if a registered route and the spec ever disagree |
| Server | Go, `server/` (~20k lines): ingest, rules, mail/search/events, Gmail, Cloudflare, health, backfill, retention, Alfather MCP |
| Edge | Cloudflare Email Worker, `edge/` (forward first, store in R2, signed notify) |
| Tests | 148 Go tests in 22 packages (all pass with Postgres and the S3 stand-in), 3 fuzzers, 8 Worker tests, 47-step end-to-end run with the real Worker code (`make e2e`), 59-check production-like smoke test (`make prod-smoke`) |
| Verified on this machine | `make lint`, `make test-integration`, `make e2e`, `make edge-test`, `actionlint` on the workflows, `docker compose config` on the production stack, a sweep of every route on a minimally configured server (no unexpected 5xx, no panics) |

Commands: `make up` (nobody runs `make down` any more), `make dev-server-start|dev-server-stop`, `make ios-e2e`, `make test`, `make test-integration`, `make e2e`, `make dev-server` (real server for the simulator, `docs/DEV_SERVER.md`),
`make ci`, `make docker-build`, `make prod-smoke` (image + prod compose + backup/restore, own project `inbox-prod-smoke`), `make edge-dryrun` (Worker bundle check, offline). Deploy: `docs/DEPLOY.md`. Operate: `docs/RUNBOOK.md`.

## Done and tested

Everything below is covered by integration tests against real Postgres and an S3-compatible store, with local fakes for Google and Cloudflare.
The end-to-end run exercises the whole chain with a real `inboxd` process.

- **Ingest**: signed notify (replay-safe), MIME parsing (charsets, inline CIDs, attachments, missing Message-ID), threading, dedupe, `store_failed` and `parse_failed` rows with plain reasons. The notify route is served by its own listener (`INGEST_ADDR`, default `:8081`, nothing else on it); the API listener does not serve it.
- **Catch-up from R2** (D-020): `reconcile_r2` lists `raw/` and ingests what Postgres lacks at startup, every `RECONCILE_INTERVAL` (default 5m) and on `POST /v1/mail:refresh`; resumes after a saved cursor (`settings.reconcile_cursor`, only past objects older than 1h) with a full list-and-diff daily; tests include a missed-ping scenario, cursor/full-scan behaviour and an e2e step.
- **Auth** (D-012): `POST /v1/auth/login` with the owner password (`OWNER_PASSWORD`, at least 12 characters or the server will not start; hashed in memory with argon2id and a random salt, constant-time compare, nothing stored), login rate limit (per-address escalating lock + global cap, 429 + `Retry-After`, generic 401), device tokens (only a hash stored), revoke, per-device rate limit, idempotency keys. Migration 0005 dropped `devices.apple_sub` and the owner/bootstrap settings.
- **Mail**: businesses, messages, threads (including your own sent replies), body/raw/attachments (presigned), batch read/archive with undo window, search with operators, recents, live events (SSE), Needs-attention lists.
- **Rules**: matcher with plus-tag stripping, RE2 validation with column errors, tester, ordering, re-routing, Gmail-filter import (label-only).
- **Gmail**: OAuth with PKCE, encrypted refresh token, `invalid_grant` handling (needs re-auth, queue paused, resumes on reconnect), write-back engine (labels, archive, read; undo; 6 tries over ~16 min; rate-limit pause), labels, filters, sent-reply import, retry endpoints, failed mail re-fetched from Gmail.
- **Cloudflare/health**: zone list, per-domain routing check, business creation validated against Cloudflare, `/v1/health` with `fix_route`, hourly routing check, 5-minute snapshot with `health.changed` and a dead-man ping.
- **Backfill**: estimate, one import per business, exact pause/resume across rate limits and sign-outs, cancel, progress events, label-only write-back.
- **Privacy**: prefs, nightly retention prune, delete-all-stored-mail behind a typed code, business removal purges rows and objects.
- **Alfather**: pairing/status/unpair, MCP over streamable HTTP (`next_messages`, `ack`, `get_message`, `search`), at-least-once cursor.
- **Production hardening (round C)**: `INBOX_ENV=production` (the image default) makes `serve` refuse missing or weak settings and log every problem by variable name (never a value); `/healthz` is liveness, `/readyz` checks Postgres and the R2 bucket (names the failed one, R2 result cached 30 s); migrations run at every start under a Postgres advisory lock (idempotent, forward-only; `inboxd migrate` still exists); graceful SIGTERM drain (listeners, then River, then the pool; exit 0);
  server timeouts (header 10 s, read 60 s, write 2 min, idle 2 min; the ingest listener 10 s/15 s/30 s; SSE clears its own deadline), a 4 MiB request body cap on the API plus tighter per-handler caps, 64 KiB on ingest; a test runs the real `serve()` and fails if a password, device token, HMAC secret/signature, Google secret/code, encryption key, S3 secret or raw mail body reaches the log.
- **Sign-in resumes setup**: the login response carries optional `setup_step` (`domains` no business yet, `gmail` business but no Gmail, `done` Gmail connected; backfill does not gate `done`) (C-19).
- **Gmail scopes**: the consent asks for `gmail.modify` and `gmail.settings.basic` together (D-015), covered by tests.
- **Ops**: Dockerfile (non-root, `INBOX_ENV=production`), production compose (`deploy/docker-compose.prod.yml`: pinned `postgres:16.6-alpine` with a named volume + the server; restart policies, health checks, read-only root, dropped capabilities, log rotation, no `:latest`, no default image, no resource limits), `deploy/.env.example` (every variable, secrets empty), `scripts/backup.sh` / `scripts/restore.sh` (+ `deploy/launchd/com.inbox.backup.plist.example`), `scripts/prod-smoke.sh`, CI/image workflows (nightly + manual `prod-smoke` job in `ci.yml`; `image.yml` also checks non-root and the refusal to start, and prints the digest to pin; deploy workflow = Worker only), runbook, deploy guide, dev server, e2e script.

## Stubbed, assumed or unverified

| Item | Status | Where |
|---|---|---|
| **Alfather's real contract** | Assumed: server is the MCP server, Alfather connects out with a bearer token from pairing; tool names/shapes are mine. Replace when Alfather's actual pairing/MCP needs are known | Q-T31-1, `/mcp` in the spec |
| **Gmail scopes** | Both `gmail.modify` and `gmail.settings.basic` are requested up front (D-015). That Google's consent screen and the real filters API accept this has not been seen against Google | D-015, DEPLOY §8 |
| **`message.raw` after `forward()`** | Worker forwards first, then reads the raw stream. Unknown whether the real runtime allows it; the failure path (`store_failed`, retry from Gmail) covers it, but test on staging | Q-T02-2 |
| **Real services** | Never exercised: Cloudflare API, Google OAuth/Gmail, R2, Tailscale Serve/Funnel, GitHub Actions. All behaviour is against fakes that follow the documented APIs | D-007 |
| **Tailscale** | `serve`/`funnel` flags checked against `--help` of Tailscale 1.104.2 only; nothing run. The `funnel` node attribute syntax and the Funnel public ports (443, 8443, 10000) come from docs. Whether Serve forwards the caller address in `X-Forwarded-For` (for per-client login lockout, `LOGIN_IP_HEADER`) is unknown | D-020, DEPLOY §6 |
| **Mac mini** | The compose stack, image, backup and restore scripts ran locally (`make prod-smoke`, Docker on macOS). `pmset` flags, Docker autostart and the launchd plist (linted with `plutil`) were not run on the Mac mini | DEPLOY §1, §11, RUNBOOK |
| **Worker in e2e** | Resolved: `make e2e` runs the real `edge/src/index.ts` in Node (R2 and Email Routing are stand-ins; `message.raw`-after-`forward()` behaviour of the real runtime is still unverified) | Q-B5-1 |
| **Gmail backfill search** | `to:@domain` plus header matching: mail that reached you only by Bcc or an alias that is not on the domain is not found | Q-T25-1 |
| **Fix routing** | The server only reads Cloudflare; "fix" is instructions plus a re-check | Q-T22-1 |
| **CI and image workflows** | `ci.yml`, `image.yml` (multi-arch image to ghcr.io with SBOM, provenance and attestation) and `deploy.yml` (deploys only the edge Worker) pass `actionlint` and parse as YAML but have never run on GitHub: there is no remote and none was created. First run will probably need small fixes (permissions, package visibility; see DEPLOY step 3) | Q-T28-1, B-1 |
| **`make ios-e2e`** | Server half done and verified (detached dev server, richer seed, env passed to the runner); the `InboxLiveTests` target is Swift's, so the xcodebuild half was only dry-run | Q-B6-1 |
| **Dev token** | `make dev-server` registers a public token; local only, CLI-only (`inboxd admin dev-token`), never an HTTP route | `docs/DEV_SERVER.md` |

## Known limits (accepted, not bugs)

- One server instance only: live events, the Gmail engine and scheduled jobs are per process (Q-T09-3). SSE has no replay: a client that reconnects refetches.
- Idempotency keys are claimed before the handler runs (a simultaneous duplicate gets 409 `request_in_progress`); the request body is not compared, so reusing a key for a different request replays the first answer.
- Gmail token encryption supports several keys (`TOKEN_ENC_KEYS`) and `inboxd admin rotate-keys`; there is nothing else sealed with them.
- Any paired device may revoke any device (single-owner server).
- Search is paginated (`limit` 1-200, default 100, `cursor`); each page is grouped by business on its own and the client merges groups. Cursors are tied to the same query.
- Idempotency does not compare request bodies: reusing a key for a different request replays the first answer.
- Failed rows created before the Message-ID was kept (before T25) cannot be re-fetched from Gmail (409 `not_in_gmail`).
- Per-job timeouts are River's default (1 minute); backfill yields and continues, ingest of very large mail relies on the 25 MiB Email Routing cap.
- The server reads `FORWARD_TO` (listed in the plan's config) but does not use it; it is a Worker setting.
- Backups are scripts you schedule (`scripts/backup.sh`, launchd example), not something the server does. They cover Postgres only; `deploy/.env` (including `TOKEN_ENC_KEYS`) is not in the dump. R2 holds every raw message and `reconcile_r2` rebuilds the message index, but businesses, rules, devices, the Gmail connection and read/archive state exist only in Postgres (RUNBOOK "Backups").
- The API listener has no per-endpoint write timeout beyond 2 minutes and no request logging; there is no metrics endpoint.
- Rolling back across a migration means restoring the pre-upgrade backup (migrations are forward-only).
- The login lock is in memory (a restart clears it) and a flood of wrong guesses can briefly lock the owner out too (global cap); behind Serve, without `LOGIN_IP_HEADER`, all phones share one "address".
- Catch-up re-ingests any raw object Postgres has no message or ingest-log line for. If a deleted message's object survived a failed blob purge, the next full scan can bring it back (rare; the purge normally succeeds).
- Devices paired with Sign in with Apple before migration 0005 keep their tokens; revoke them if you want everyone to sign in again with the password.

## What you need to do

`docs/DEPLOY.md` is the single ordered checklist (12 steps, each with a Verify line). Only the owner can do these:

1. **GitHub**: create the repository and push; allow workflow write permissions; set package visibility or log the Mac mini in to ghcr.io; tag `v0.1.0` (DEPLOY step 3). Nothing was pushed or created for you.
2. **Mac mini**: Docker at login, Tailscale signed in, `pmset` settings (step 1).
3. **Cloudflare**: R2 bucket and token, read-only API token, verified destination address, Worker deploy with `wrangler secret put INGEST_SECRET` (steps 4 and 7), then Email Routing per domain (step 10).
4. **Tailscale**: `serve` and `funnel` commands, the `funnel` policy attribute (step 6).
5. **Google**: OAuth client, both Gmail scopes on the consent screen, app published "In production" (step 8).
6. **Secrets**: fill `deploy/.env` (step 2); the server tells you by name what is missing.
7. **Run the Worker spike (Q-T02-2) on a spare domain before any real domain**, then switch domains one at a time (steps 7 and 10).
8. **Schedule the backup** and rehearse one restore (step 11).
9. **Decide** the open questions in `DECISIONS.md` that need you (retention default, Alfather's real contract).

## Where things live

`server/cmd/inboxd` (serve | migrate | admin seed-demo|dev-token|rotate-keys), `server/cmd/e2e` (end-to-end driver), `server/internal/<package>` (see the plan §7.1; `gmail/gmailtest` and `cloudflare/cftest` are the fakes),
`server/db/migrations` (0001-0005), `edge/`, `deploy/` (`docker-compose.prod.yml`, `.env.example`, `Dockerfile`, `launchd/`; `docker-compose.yml` is the test stack), `scripts/` (`e2e.sh`, `dev-server.sh`, `prod-smoke.sh`, `backup.sh`, `restore.sh`, `openapi_add.py`), `.github/workflows/`.
