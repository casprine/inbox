
## 2026-10-12 — B-4 search pagination (landed, api/openapi.yaml)
`GET /v1/search?q=…&limit=50&cursor=…` (limit 1–200, default 100 = old behaviour). Response now also has `has_more` (bool) and `next_cursor` (string|null).
Pass `next_cursor` back as `cursor` for the next, older page; each page is grouped by business on its own, so merge groups by business id (unassigned = business null).
`total` = results on THIS page. Bad `limit`/`cursor` → 400 `invalid_limit` / `invalid_cursor`. Cursor only valid with the same `q` and `business`.
Real samples: run `make dev-server` and call it (demo mail is small; use limit=2 to see paging).

## 2026-10-12 — B-6 live-test enablers (landed)
`make dev-server-start` / `make dev-server-stop` (detached, idempotent, prints URL+token), `scripts/dev-server.sh status`. Seed now has failed items (3 kinds), a thread with your sent reply, remote-image mail, a rule-routed order, a paused rate-limited import. Table: docs/DEV_SERVER.md "Demo data".
`make ios-e2e` is wired: start server → `xcodebuild … -only-testing:InboxLiveTests test` (env INBOX_API_BASE_URL / INBOX_DEV_TOKEN via TEST_RUNNER_) → always stop. Needs your `InboxLiveTests` target in project.yml.
Add `-skip-testing:InboxLiveTests` to `ios-test` and `ios-ui-test` in the Makefile yourself (or tell me) so the opt-in suite never runs there.

## 2026-10-31 — Password sign-in replaces Sign in with Apple (D-012, landed in server/ and api/openapi.yaml)

**`POST /v1/auth/apple` is gone.** There is no Apple token, no `bootstrap_code`, no owner claim, no `403 not_owner` / `bootstrap_required` / `invalid_identity_token` any more. The sign-in is one password the owner sets on the server (`OWNER_PASSWORD`).

**Login** — public (no bearer), JSON:
```
POST /v1/auth/login
Content-Type: application/json
{"password": "<the owner password>", "device_name": "Casprine's iPhone"}      // device_name optional, max 100 chars

200 {"device_id": "<uuid>", "token": "<bearer token, shown once>", "setup_step": "domains|gmail|done"}
```
`setup_step` is optional (C-19): where first-run setup stands, computed from server state. `domains` = no business exists yet, `gmail` = a business exists but Gmail is not connected, `done` = Gmail is connected (importing old mail is optional and does not gate `done`). Treat a missing or unknown value as "unknown" and fall back to your own guess. Gmail connect now asks for both `gmail.modify` and `gmail.settings.basic` in one consent (D-015); nothing changes in the app flow.
Store `token` in the Keychain and send it as `Authorization: Bearer <token>` exactly as before. Do not keep the password. Device tokens, `DELETE /v1/devices/{id}` (revoke), the per-device rate limit (429 `rate_limited`, `Retry-After: 1`) and `Idempotency-Key` behave exactly as before. Devices paired before this change keep working.

Errors (all `application/problem+json`, `code` is stable):
| Status | `code` | Meaning for the UI |
|---|---|---|
| 400 | `invalid_request` | body is not JSON or `password` is empty |
| 401 | `invalid_credentials` | wrong password. Always the same generic answer: say "Incorrect password", never hint how close |
| 429 | `rate_limited` | too many attempts. Header `Retry-After: <whole seconds>`. Show "Try again in N seconds" (it can be up to 15 minutes after repeated wrong passwords) and disable the button until then. While locked, even the correct password gets 429 |
| 500 | `internal` | server problem |

Lockout rule (so the UI can be honest): per client address, the first 4 wrong passwords answer 401, the 5th still answers 401 but starts a lock; the next attempt gets 429 with the remaining seconds, and each further failure doubles the wait (5 s, 10 s, 20 s ... capped at 15 min). A correct password clears it. There is also a global cap of about 1 attempt per second for everyone (429 with `Retry-After: 1` or more). Network errors/timeouts are not rate limits: retry normally.

**Base URL.** The API is published on the owner's tailnet with Tailscale Serve, e.g. `https://mini.tail1234.ts.net` (port 443, no port in the URL, no path prefix). The phone must be connected to Tailscale to reach it. The first-run screen therefore needs two fields: server address and password. Everything under `/v1` is on that host; `/ingest/v1/notify` is NOT on it (it lives on a separate public listener and is only for the Cloudflare Worker, never for the app). `INBOX_API_BASE_URL` / `INBOX_DEV_TOKEN` keep working for debug builds and the dev server (docs/DEV_SERVER.md); the dev server also accepts the login with password `dev-password-inbox-local`.

**New: `POST /v1/mail:refresh`** (bearer, optional `Idempotency-Key`) — for pull-to-refresh. Asks the server to list its raw-mail bucket (R2) and ingest any mail whose Worker ping never arrived. Returns `202 {"status":"queued"}` immediately; there is no result body. New mail then arrives through the normal `mail.new` events on `/v1/events` and the list endpoints, so: call it, then refetch/keep listening; do not wait on the response for results. It is cheap and de-duplicated on the server (taps while one is queued collapse). `503 queue_unavailable` / `enqueue_failed` if the job queue is down. The server also does this by itself at startup and every few minutes, so calling it is optional, not required for correctness.

**Removed fields/behaviour:** nothing in other responses changed. `devices` no longer carry an Apple subject (it was never in the API).
