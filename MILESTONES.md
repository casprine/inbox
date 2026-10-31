# Inbox — Milestone Plan (execution plan)

Derived from `ENGINEERING_PLAN.md` (the design/architecture reference; read the sections cited per task). Screens: the "Inbox: mobile design" canvas, summarised in the plan's §1 and §9.

**How work runs:** a coordinator spawns one short-lived agent per task card below, strictly one at a time. Every card is small enough to finish in one sitting, ends with a runnable verification, and leaves the repo green. Status is tracked here. Anything needing the owner goes to `DECISIONS.md`.

Status legend: `[ ]` todo · `[~]` in progress · `[x]` done and verified · `[!]` blocked/needs owner (see DECISIONS.md)

---

## Shared conventions (every agent must follow)

**Repo root:** `/Users/casprine/Desktop/vendor/personal/inbox` (not a git repo; do not run git commands that need one).

```
api/openapi.yaml          hand-maintained contract
edge/                     Cloudflare Email Worker (TypeScript, vitest)
server/                   Go module  github.com/casprine/inbox/server   (go 1.25)
  cmd/inboxd/             main (subcommands: serve | migrate | admin)
  internal/<pkg>/         see ENGINEERING_PLAN.md §7.1
  db/migrations/*.sql     goose-style (-- +goose Up / Down), applied by `inboxd migrate`
ios/                      XcodeGen project.yml + Inbox app target + InboxKit local package
deploy/                   Dockerfile, fly.toml, docker-compose.yml, CI
docs/                     runbook etc.
```

- **Deviations from ENGINEERING_PLAN.md (decided, see DECISIONS.md D-001..):** no sqlc / oapi-codegen / swift-openapi-generator. Hand-written `pgx` queries, `chi` handlers, hand-written Swift `Codable` client. `api/openapi.yaml` is maintained by hand and checked by a Go contract test (every route registered in chi must appear in the spec and vice versa).
- **Local infra:** `deploy/docker-compose.yml` runs Postgres 16 (`postgres://inbox:inbox@localhost:54329/inbox?sslmode=disable`) and SeaweedFS S3 (was MinIO; API `http://localhost:9009`, bucket `inbox-mail`, creds `minio`/`minio12345`). Tests that need them read `TEST_DATABASE_URL` and `TEST_S3_ENDPOINT` and **skip** (not fail) when unset; `make test-integration` sets them and starts compose.
- **Config (env):** `DATABASE_URL`, `HTTP_ADDR` (default `:8080`), `PUBLIC_BASE_URL`, `INGEST_SECRET`, `S3_ENDPOINT`, `S3_BUCKET`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_REGION` (`auto`), `TOKEN_ENC_KEY` (base64 32 bytes), `APPLE_AUDIENCE`, `OWNER_APPLE_SUB`, `BOOTSTRAP_CODE`, `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `CF_API_TOKEN`, `CF_ACCOUNT_ID`, `FORWARD_TO`. Parsed in `internal/config`; missing optional integrations disable that feature, never crash the server.
- **IDs:** UUIDv7 generated in Go (`github.com/google/uuid` `NewV7`). Times are UTC `timestamptz`.
- **Errors:** RFC 9457 `application/problem+json` with stable `code` (snake_case). JSON field names snake_case.
- **Auth:** `Authorization: Bearer <device token>` on `/v1/*`; `Idempotency-Key` on mutating calls.
- **Code style:** match surrounding code; small packages; table-driven tests; no global state; context everywhere; `slog`. Comments only where the why is non-obvious.
- **Swift:** Swift 6, iOS 17+, SwiftUI + Observation, GRDB. Design tokens/components per ENGINEERING_PLAN §8.5 and the design summary. Fonts are not bundled yet (D-0xx): use `Font.custom` with system fallback.
- **Design reference:** `docs/design-ref/*.dc.html` are HTML exports of ~45 key boards (390×844). They are large (mostly CSS) — never print them whole; extract text/styles with a script (strip `<helmet>`/`<style>`) or grep for the `<x-dc>` body. The per-board behaviour summary is in ENGINEERING_PLAN.md §1 and §9.
- **Agent rules:** stay inside your task's paths; don't refactor others' code (small fixes needed to make your task compile are fine, say so in your report); run the verification commands and report their real output; if you hit a decision only the owner can make, pick a safe default, implement it, and append a short entry to `DECISIONS.md` under "Open questions" with your task id; finish within your scope even if imperfect — leave TODO notes in your report rather than expanding.
- **Report format (≤150 words):** what was built (paths), verification commands + result, deviations, follow-ups.

---

## M0 — Foundations

- [x] **T01 Monorepo skeleton.** Create dirs, root `Makefile` (`make build test test-integration lint up down`), `.gitignore`, `README.md` (how to run), `deploy/docker-compose.yml` (Postgres+MinIO+bucket-init), Go module with `cmd/inboxd` (`serve` with `/healthz` `/readyz`, `migrate`), `internal/config`, goose-style migration runner (embedded SQL, own tiny runner or `pressly/goose` lib), empty `0001_init.sql` placeholder, `api/openapi.yaml` skeleton with `/healthz`. *Verify:* `cd server && go build ./... && go test ./...`; `make up && inboxd migrate && curl /healthz`.
- [x] **T02 Edge worker.** `edge/` per plan §6: `email()` forwards first, R2 put with `FixedLengthStream` (fallback buffer path if `FixedLengthStream` unavailable), HMAC-signed `notify()` with 3 retries via `waitUntil`, failure notify (`reason=store_failed`), `wrangler.jsonc`, vitest unit tests with mocked message/R2/fetch (forward-first ordering, never rethrows, signature format `X-Inbox-Timestamp`/`X-Inbox-Signature` = hex HMAC-SHA256 of `timestamp + "." + body`). *Verify:* `cd edge && npm test && npx tsc --noEmit`.

## M1 — Ingest

- [x] **T03 Schema + store.** `0001_init.sql` from plan §5 (all tables, enums, indexes; `pg_trgm` extension), `internal/store` (pgx pool, tx helper, typed query funcs for businesses, messages, attachments, ingest_log, settings). Integration tests. *Verify:* `make test-integration`.
- [x] **T04 Blob + crypto + ids.** `internal/blob` (S3 client: Put/Get/List/Delete/Presign, MinIO-compatible), `internal/crypto` (AES-256-GCM seal/open with key id, token generation + sha256 hashing), `internal/ids`. Unit + integration tests.
- [x] **T05 MIME parse + threading.** `internal/ingest/parse` using `enmime`: headers, from/to, subject, text/html bodies, snippet, attachments, inline CIDs, auth results (Authentication-Results → spf/dkim/dmarc), Message-ID fallback `sha256:`; `thread_key` logic; fixtures in `server/internal/ingest/testdata/*.eml` (plain, multipart/alternative, inline cid, attachment, non-UTF-8 charset, missing Message-ID, reply with References). Pure-function tests.
- [x] **T06 Notify endpoint + ingest job.** HMAC middleware + `POST /ingest/v1/notify` (202, idempotent), River setup (`river` + `riverpgxv5`, migrations via River's migrator inside `inboxd migrate`), `ingest_process` worker (R2 get → parse → insert message/attachments/addresses/search row → route stub `unrouted|pending` → log lines → `NOTIFY mail_new`), `reconcile_r2` periodic job, failed-row path for `reason=store_failed`. *Verify:* integration test: post notify with fixture in MinIO → message row + search row exist; duplicate is no-op; reconcile imports an orphan key.

## M2 — Read path

- [x] **T07 Auth.** `internal/auth`: Apple identity-token verification (JWKS fetch with cache; injectable for tests), `POST /v1/auth/apple` (owner allow-list via `OWNER_APPLE_SUB`, first-claim with `BOOTSTRAP_CODE`), device token issue (32B random, sha256 stored), bearer middleware, `DELETE /v1/devices/{id}`, per-device rate limit, `Idempotency-Key` middleware (table-backed), problem+json helpers. `inboxd admin bootstrap` prints a code. *Verify:* httptest suite with a test-signed JWT.
- [x] **T08 Businesses + messages API.** Endpoints in plan §7.2 for businesses (CRUD-lite: list with unread counts, create, patch, order, delete w/ blob purge) and messages (list w/ keyset cursor + chips filter, get w/ thread+attachments+details, body, raw, attachment redirect, `POST /messages:batch` read/unread/archive/unarchive with `gmail_sync` upsert hook stub). Seed command `inboxd admin seed-demo` loads fixtures. *Verify:* integration tests + curl script.
- [x] **T09 Search + SSE + attention.** `internal/mail/search` operator parser (`from:` `to:` `has:attachment`, quotes, prefix match) → SQL; `/v1/search` grouped by business; recents endpoints; `/v1/events` SSE via LISTEN/NOTIFY hub with heartbeat; `/v1/attention*` (unrouted/failed lists, retry/dismiss stubs that re-enqueue `ingest_process`). Parser unit tests + integration.
- [x] **T10 OpenAPI + contract test.** Fill `api/openapi.yaml` for everything built so far (schemas, errors, auth), add the route↔spec contract test. Keep it updated by later tasks (each later server task must add its routes — the test enforces it).

## M2b — iOS shell and read path

- [x] **T11 iOS project + design system.** XcodeGen `ios/project.yml`, app target, `InboxKit` package with `DesignSystem` (Palette light/dark from plan §8.5, Theme environment, font helpers, components: MailRow incl. skeleton, Chip, InsetGroup/Row, SquareSwitch, Banner, Toast, EmptyState, StatusDot, ProgressBar, Tab bar with accent bar), a gallery preview screen. *Verify:* `xcodebuild build` for iPhone 17 Pro simulator; unit tests pass.
- [x] **T12 API client + persistence + auth.** `API` module (URLSession client, bearer + idempotency, problem decoding, SSE client), `Persistence` (GRDB schema from plan §8.2, migrations), Keychain token store, `AppEnvironment` composition, a `MockServer` URLProtocol for tests. Repositories skeleton (Business, Mail) SWR pattern + outbox.
- [x] **T13 Mail feature.** Businesses root, business page with chips, All businesses, swipe actions, select mode, pull to refresh, skeleton/empty states, archive toast + undo (outbox cancels), Needs-attention group, tab bar shell wiring. Model tests with MockServer.
- [x] **T14 Message feature.** Message view (header, body, attachments, bottom toolbar), thread collapse, `MessageWebView` (JS off, content rules, cid scheme handler, load-images-for-sender), More sheet, Details, Original, Gmail-not-found; hostile-HTML security tests (zero network requests / JS).
- [x] **T15 Search feature.** Search idle/results/scoped/none, recents, operator hints, highlighting.

## M3 — Rules

- [x] **T16 Rules engine + API.** `internal/rules` (model, matcher w/ plus-tag stripping, RE2 validation with column errors, tester with caught-earlier detail, label-only effect), endpoints (CRUD, order, test incl. `scope=unrouted`, apply), wire routing into `ingest_process`, `reroute_unrouted` job, Gmail-sync `want_*` computation. Exhaustive table tests + fuzz.
- [x] **T17 iOS Rules + Attention.** Rules list/edit/reorder/delete/none, editor (with invalid-pattern state), label picker sheet (server labels stubbed until M4), test results, Unrouted + rule-from-message sheet, Failed list/detail/retry/dismiss.

## M4 — Gmail

- [x] **T18 Gmail OAuth + account.** `internal/gmail`: oauth config, state/PKCE store, `POST /gmail/connect`, `GET /oauth/google/callback` (redirect `inbox://gmail/connected`), encrypted token storage, token source w/ `invalid_grant` → `needs_reauth` + SSE, status, disconnect. Fake Google endpoints in tests.
- [x] **T19 Write-back engine.** Limiter, lookup by `rfc822msgid`, label resolve/create + cache, `gmail_writeback` job state machine (plan §4.3: 6 tries over ~15 min, pause/resume, rate-limit snooze), archive undo window (8 s), batch-action integration, retry endpoint. Fake Gmail httptest server with scripted failures.
- [x] **T20 Labels, filters import, thread hydrate.** `GET/POST /gmail/labels`, `GET /gmail/filters` + `POST /rules:import` (label-only, delete filters flagged non-importable), `thread_hydrate` job importing SENT replies as `direction=out`.
- [x] **T21 iOS Gmail.** Settings→Gmail (connected, signed-out banner, reconnect, disconnect alert, catching up), connect via `ASWebAuthenticationSession`, deep link handling, label picker wired, import-from-Gmail screen.

## M5 — Setup and health

- [x] **T22 Cloudflare + businesses + health.** `internal/cloudflare` client (zones, catch-all rule), `GET /cloudflare/domains`, routing check job + endpoint, business add/remove semantics, `internal/health` checks + `/v1/health` with `fix_route`, `health_snapshot` periodic job. Fake CF server in tests.
- [x] **T23 iOS onboarding.** OB01–OB13 flow with Sign in with Apple, domain picker with Fix routing sheet, Gmail steps (skippable), filters, import choice (estimates stubbed until M6), Alfather discovery (Bonjour browse, stub pairing), done + first-open import banner. Resumable step state.
- [x] **T24 iOS Businesses + Settings + Health.** Settings root, Businesses list/edit/detail/add/remove/routing-problem, Appearance, Privacy, About, Health ok/needs-you, Alfather status screens (against server stubs).

## M6 — Backfill

- [x] **T25 Backfill server.** `internal/backfill`: estimate, create per business, `backfill_page` job (dedupe, recipient resolution, label-only write-back, pause on 429), pause/resume/cancel, SSE `job.progress`, retry-failed-via-Gmail fetch path. Fake Gmail tests.
- [x] **T26 iOS import screens.** Import older mail setup/running/done/paused; onboarding step wired to real estimate/create; first-open banner via SSE.

## M7 — Hardening and ship

- [x] **T27 Retention + purge + prefs.** `prune` job, `/prefs`, `DELETE /data` with confirmation token, remove-business purge verification; iOS Privacy wired.
- [x] **T28 Deploy + CI + runbook.** `deploy/Dockerfile`, `fly.toml`, GitHub Actions (server, edge, openapi contract, ios build), staging/prod notes, `docs/RUNBOOK.md`, `docs/DEPLOY.md` (Cloudflare, Fly, Neon, Google OAuth, Apple SIWA steps for the owner).
- [x] **T29 End-to-end + iOS snapshot/UI tests (server e2e 40 checks; iOS 135 unit incl. 28 snapshot baselines + 4 UI; re-verified by coordinator).** Script `scripts/e2e.sh`: compose up, migrate, seed, run server, push fixture through a local simulated Worker notify, assert API results. iOS: snapshot tests for key boards in light/dark, UI smoke test.
- [x] **T30 Final audit.** Fresh-clone build of everything, full test run, openapi contract, lints, README accuracy, DECISIONS.md tidy, and a "state of the project" doc listing what's done, what's stubbed, and what the owner must do (accounts, secrets, spikes).

## M8 — Alfather

- [x] **T31 Alfather MCP server + iOS pairing (contract assumed, Q-T31-1/3).** `internal/alfather`: consumer tokens, pairing endpoints, MCP (streamable HTTP) with tools `next_messages`, `ack`, `get_message`, `search`, status endpoint; iOS Alfather pairing against it (assumed contract documented in DECISIONS.md).
