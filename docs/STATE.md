# State of the backend

What exists, what was tested and how, what is assumed or stubbed, and what only you can do. Written at the end of the backend queue
(T18-T29 server half plus the T30 server audit); updated after Phase 2 backend work (B-1 to B-7) and the 2026-10-31 hosting change (D-012 password sign-in, D-018 to D-020: Mac mini, Docker, Tailscale, R2 catch-up). The iOS app has its own status. Decisions behind everything here: `DECISIONS.md` (Q-T*, D-*).

## At a glance

| | |
|---|---|
| API | 61 operations on 52 paths in `api/openapi.yaml` (`/v1/auth/login` replaces `/v1/auth/apple`; new `/v1/mail:refresh`); a Go test fails if a registered route and the spec ever disagree |
| Server | Go, `server/` (~20k lines): ingest, rules, mail/search/events, Gmail, Cloudflare, health, backfill, retention, Alfather MCP |
| Edge | Cloudflare Email Worker, `edge/` (forward first, store in R2, signed notify) |
| Tests | Go tests + 3 fuzzers (20 packages) including password login, lockout and catch-up tests, 8 Worker tests, 47-step end-to-end run with the real Worker code (`make e2e`) |
| Verified on this machine | `make lint`, `make test-integration`, `make e2e`, `make edge-test`, `actionlint` on the workflows, `docker compose config` on the production stack, a sweep of every route on a minimally configured server (no unexpected 5xx, no panics) |

Commands: `make up` (nobody runs `make down` any more), `make dev-server-start|dev-server-stop`, `make ios-e2e`, `make test`, `make test-integration`, `make e2e`, `make dev-server` (real server for the simulator, `docs/DEV_SERVER.md`),
`make ci`, `make docker-build`. Deploy: `docs/DEPLOY.md`. Operate: `docs/RUNBOOK.md`.

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
- **Ops**: Dockerfile, production compose (`deploy/docker-compose.prod.yml`: Postgres container + migrate + server), CI/image workflows (deploy workflow = Worker only), runbook, deploy guide, dev server, e2e script.

## Stubbed, assumed or unverified

| Item | Status | Where |
|---|---|---|
| **Alfather's real contract** | Assumed: server is the MCP server, Alfather connects out with a bearer token from pairing; tool names/shapes are mine. Replace when Alfather's actual pairing/MCP needs are known | Q-T31-1, `/mcp` in the spec |
| **Gmail filters scope** | Only `gmail.modify` is requested. Whether it covers `filters.list` is unknown; if not, add `gmail.settings.basic` (one line, see DEPLOY §5) | Q-T18-2 |
| **`message.raw` after `forward()`** | Worker forwards first, then reads the raw stream. Unknown whether the real runtime allows it; the failure path (`store_failed`, retry from Gmail) covers it, but test on staging | Q-T02-2 |
| **Real services** | Never exercised: Cloudflare API, Google OAuth/Gmail, R2, Tailscale Serve/Funnel, GitHub Actions. All behaviour is against fakes that follow the documented APIs | D-007 |
| **Tailscale** | `serve`/`funnel` flags checked against `--help` of Tailscale 1.104.2 only; nothing run. The `funnel` node attribute syntax and the Funnel public ports (443, 8443, 10000) come from docs. Whether Serve forwards the caller address in `X-Forwarded-For` (for per-client login lockout, `LOGIN_IP_HEADER`) is unknown | D-020, DEPLOY §6 |
| **Mac mini** | Sleep settings, Docker autostart and the backup/restore commands in the docs are written from general knowledge and not run | DEPLOY §2, §5, RUNBOOK |
| **Worker in e2e** | Resolved: `make e2e` runs the real `edge/src/index.ts` in Node (R2 and Email Routing are stand-ins; `message.raw`-after-`forward()` behaviour of the real runtime is still unverified) | Q-B5-1 |
| **Gmail backfill search** | `to:@domain` plus header matching: mail that reached you only by Bcc or an alias that is not on the domain is not found | Q-T25-1 |
| **Fix routing** | The server only reads Cloudflare; "fix" is instructions plus a re-check | Q-T22-1 |
| **CI and image workflows** | `ci.yml`, `image.yml` (multi-arch image to ghcr.io with SBOM, provenance and attestation) and `deploy.yml` (now deploys only the edge Worker; the Fly job was removed) pass `actionlint` and parse as YAML but have never run on GitHub: there is no remote and none was created. First run will probably need small fixes (permissions, package visibility; see DEPLOY "Container image on GitHub") | Q-T28-1, B-1 |
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
- Nothing is backed up by this repo: take `pg_dump` backups of the compose Postgres volume yourself (DEPLOY §5); R2 holds every raw message and `reconcile_r2` re-adopts whatever a restored database lacks.
- The login lock is in memory (a restart clears it) and a flood of wrong guesses can briefly lock the owner out too (global cap); behind Serve, without `LOGIN_IP_HEADER`, all phones share one "address".
- Catch-up re-ingests any raw object Postgres has no message or ingest-log line for. If a deleted message's object survived a failed blob purge, the next full scan can bring it back (rare; the purge normally succeeds).
- Devices paired with Sign in with Apple before migration 0005 keep their tokens; revoke them if you want everyone to sign in again with the password.

## What you need to do

In order; the detailed steps are in `docs/DEPLOY.md`.

1. **GitHub**: create the repository and push when you want CI and the container image (nothing was pushed or created for you); set package visibility (DEPLOY §4).
2. **Mac mini**: Docker, Tailscale signed in, sleep disabled. Accounts: Cloudflare (domains, R2, Email Routing), Google Cloud (OAuth client, app published "In production"), optionally a dead-man URL (healthchecks.io-style).
3. **Secrets**: generate `INGEST_SECRET`, the Gmail token key (`TOKEN_ENC_KEYS=k1:<base64>`, `openssl`), choose `OWNER_PASSWORD` (12+ characters), create the R2 token, the Cloudflare read-only API token, the Google client id/secret; put them in `deploy/.env` and `wrangler secret put`.
4. **Run it**: `docker compose -f deploy/docker-compose.prod.yml up -d`, then publish with `tailscale serve --bg --https=443 http://127.0.0.1:8080` and `tailscale funnel --bg --https=8443 http://127.0.0.1:8081` (add the `funnel` attribute to the tailnet policy first). Deploy the Worker with `INGEST_URL` = the Funnel address.
5. **Stage first** if you can, and run the spikes:
   - send a mail and confirm it arrives in both Gmail and Inbox (settles the `message.raw` question),
   - connect Gmail and open *Bring your filters* (settles the filters scope),
   - stop the server, send a mail, start it: the mail must appear by itself (catch-up),
   - leave the Google app in Testing for a week only if you want to see the 7-day expiry; otherwise publish it.
6. **Sign in** from the app with the server address and `OWNER_PASSWORD`.
7. **Switch domains one at a time** from "Send to email" to the Worker `inbox-worker`; watch the Health screen after each. Rollback is one click in Cloudflare.
8. **Decide** the open questions in `DECISIONS.md` that need you (retention default, accent colour, fonts, Alfather's real contract).

## Where things live

`server/cmd/inboxd` (serve | migrate | admin seed-demo|dev-token|rotate-keys), `server/cmd/e2e` (end-to-end driver), `server/internal/<package>` (see the plan §7.1; `gmail/gmailtest` and `cloudflare/cftest` are the fakes),
`server/db/migrations` (0001-0005), `edge/`, `deploy/` (`docker-compose.prod.yml`, `.env.example`, `Dockerfile`; `docker-compose.yml` is the test stack), `scripts/` (`e2e.sh`, `dev-server.sh`, `openapi_add.py`), `.github/workflows/`.
