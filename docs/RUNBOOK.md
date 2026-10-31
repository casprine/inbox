# Inbox runbook

For when something looks wrong. Everything the server knows about its own health is on one screen in the app (Settings → Health) and in `GET /v1/health`;
start there, then use the matching section. Commands assume the Mac mini, run from the `deploy/` folder of the repository, with
`alias dc='docker compose -f docker-compose.prod.yml'`.

Golden rule: **mail is never lost while the Worker runs.** It forwards to Gmail before doing anything else, so the worst case is "Inbox is behind", not "mail is missing".

## Quick commands

```bash
dc ps                                       # postgres + inbox must be running/healthy (migrate has exited 0)
dc logs -f inbox                            # JSON lines, no subjects/addresses/bodies at INFO
dc restart inbox                            # safe: jobs are durable and leased; also clears the login lockout memory
dc pull && dc up -d                         # update to the newest image (migrations run first)
dc exec inbox inboxd admin rotate-keys      # see "Rotate the Gmail token encryption key"
tailscale serve status; tailscale funnel status   # what is published (API on 443 for the tailnet, ingest on 8443 for the internet)
```

Liveness check from a device on the tailnet: open `https://mini.tail1234.ts.net/readyz` (answers `{"status":"ready"}`); on the Mac mini: `curl -s http://127.0.0.1:8080/readyz`.

Local development equivalents: `make up`, `make test-integration`, `cd server && go run ./cmd/inboxd serve` (needs `OWNER_PASSWORD`); nobody runs `make down`.

## Reading the Health screen

Each row has a state (ok / warn / error / off) and sometimes a button that opens the screen that fixes it. `off` means "not set up", not a problem.

### Mail arriving · <business> (error: "sends straight to email again" / "switched off")
Cloudflare's catch-all for the domain no longer runs the Worker.
1. Cloudflare → domain → Email → Email Routing → Routing rules → Catch-all: action must be **Send to a Worker → inbox-worker**, and enabled.
2. In the app, Businesses → the domain → *Check again* (`POST /v1/businesses/{id}/routing:check`).
Meanwhile mail still reaches Gmail if the catch-all forwards to your address. If it is switched off entirely, mail bounces: fix this first.

### Mail arriving (warn: "No mail for N days")
The domain is quieter than usual (threshold: longer of 3 days and 3× its typical gap). Usually just a quiet domain. Send a test mail to it; if it arrives in Gmail but not Inbox,
see "Worker" below. If it does not arrive anywhere, check the domain's MX records in Cloudflare.

### Mail worker (warn: "Running, N errors today")
`ingest_log` has error lines in the last 24 h. Open Needs attention → Failed in the app for the items; each shows what happened in plain words.
- `store_failed`: the Worker could not save the raw mail (R2 hiccup). The mail is in Gmail. **Retry** fetches it from Gmail by Message-ID (needs Gmail connected).
- `parse_failed`: the saved message could not be read. Retrying will fail again; it stays available as the raw original. Dismiss it.

### Mail worker (warn: "Nothing has come through for N hours although routing looks right")
Routing says ok but no mail arrived for much longer than usual.
1. Cloudflare → Workers & Pages → `inbox-worker` → Logs (live): send a test mail and watch for errors.
2. `INGEST_URL` / `INGEST_SECRET` mismatch shows as HTTP 401 `invalid_signature` in the Worker log: re-run `wrangler secret put INGEST_SECRET` with the same value as `INGEST_SECRET` in `deploy/.env`.
3. A Worker log of connection errors or timeouts to `INGEST_URL` means the Funnel is not reachable: `tailscale funnel status` on the Mac mini (is `8443` listed?), is the Mac mini awake and online, does the tailnet policy still carry the `funnel` node attribute. A request to the Funnel address from outside should answer `404`/`405`/`401`, not hang.
4. Even if the server or the Funnel was down, nothing is lost: the server lists R2 and imports whatever it lacks at startup, every `RECONCILE_INTERVAL` (`reconcile_r2`) and on the app's pull-to-refresh (`POST /v1/mail:refresh`). The mail is in Gmail either way.

### Gmail (error: "Signed out, N actions waiting")
Google revoked the grant (password change, revoked access, or the OAuth app was left in *Testing* and the refresh token expired after 7 days).
Open the Gmail row → *Reconnect*. Queued actions (labels, archive) resume by themselves; nothing is lost. If it keeps happening: Google Cloud console → OAuth consent screen → *Publish app*.

### Gmail (warn: "Paused by Gmail rate limits")
Gmail told us to slow down. The queue and any older-mail import wait out the window and resume on their own (status shows `rate_limited_until`). Nothing to do.

### Gmail sync of one message: "Not found in Gmail" / "Failed"
Per message, Details shows why. *Not found*: the message did not show up in Gmail within about 16 minutes (6 tries), usually because it was filtered to Trash/Spam on Gmail's side or you deleted it.
*Try again* (`POST /v1/messages/{id}/gmail:retry`) once it is in Gmail. *Failed* shows Google's error text.

### Background jobs (warn: "N jobs failed in the last day" / error: "Stuck")
```sql
-- run with: dc exec postgres psql -U inbox inbox
select kind, state, attempt, left(errors::text, 300) from river_job
 where state in ('retryable','discarded') order by id desc limit 20;
select kind, scheduled_at from river_job where state='available' order by scheduled_at limit 5;   -- oldest waiting
```
Stuck (> 15 min waiting) usually means the server is not running jobs: `dc ps`, `dc logs inbox`; a restart (`dc restart inbox`) is safe, jobs are durable and leased.
Discarded jobs are not retried automatically; a failed ingest shows up in Needs attention → Failed with a Retry button.

### Alfather (warn: "Not reachable · last seen …")
Alfather has not called the server for 5 minutes. Check it is running and on a network (the tailnet) that can reach `https://mini.tail1234.ts.net/mcp`. It never blocks mail. To re-pair: Settings → Alfather → Unpair, then pair again
(re-pairing revokes the old token).

## Common situations

**The app says the password is wrong (401 `invalid_credentials`).** Check `OWNER_PASSWORD` in `deploy/.env`. The answer is deliberately the same whether the password is wrong or close.

**The app says "too many attempts" (429 `rate_limited`, with a wait time).** Sign-in is rate limited: after four wrong passwords from one address the server locks that address, with a wait that doubles each time (5 s, 10 s, ... up to 15 min), and there is a global cap of about one attempt per second across everyone. Wait the time the app shows, or restart the server (`dc restart inbox`) to clear the lockouts. Without `LOGIN_IP_HEADER` all phones behind Tailscale Serve count as one address.

**Change the password.** Edit `OWNER_PASSWORD` in `deploy/.env` (at least 12 characters), then `dc up -d`. Phones that are already signed in keep working (they hold device tokens, not the password). To make every phone sign in again, revoke the devices: `dc exec postgres psql -U inbox inbox -c "update devices set revoked_at = now() where revoked_at is null"`.

**The server will not start ("OWNER_PASSWORD is not set" / "too short").** `dc logs inbox` says which. Fix `deploy/.env` and `dc up -d`.

**A device was lost.** Settings → Devices → revoke (`DELETE /v1/devices/{id}`), or `update devices set revoked_at = now() where name = '…';`.

**Rotate `INGEST_SECRET`.** Set the new value with `wrangler secret put INGEST_SECRET` and in `deploy/.env` (then `dc up -d`) at the same time. In between, the Worker's notifications fail with 401 and are
recovered by the next reconcile run (within `RECONCILE_INTERVAL`), so no mail is lost.

**Rotate the Gmail token encryption key** (no downtime, no reconnecting).
1. `openssl rand -base64 32` → new key. Put it **first** in the list and keep the old one: set `TOKEN_ENC_KEYS=k2:<new>,k1:<old>` in `deploy/.env` and `dc up -d` (if you still had the single `TOKEN_ENC_KEY`, it counts as `k1`; the list's first key becomes current). The server restarts and still reads the old token.
2. Re-seal under the new key: `dc exec inbox inboxd admin rotate-keys` → `current key "k2": re-sealed 1, already current 0`. Safe to repeat.
3. Only now drop the old key: `TOKEN_ENC_KEYS=k2:<new>` in `deploy/.env` (and remove `TOKEN_ENC_KEY` if set), `dc up -d`.
If you removed a key too early the command and the server say which key id the stored token needs; add it back. If a key is truly lost, reconnect Gmail in the app (mail and rules are unaffected).

**Switch a domain back to plain forwarding.** Cloudflare → catch-all → *Send to an email*. Inbox shows the routing problem for that business until you remove it or switch back.

**Delete all stored mail.** Settings → Privacy → Delete stored mail (needs the typed confirmation). Gmail itself is untouched. Or set a retention period (nightly prune at 03:00 UTC).

**Remove a business.** Settings → Businesses → Remove. Its mail, attachments and raw copies are deleted; Cloudflare is not changed, so new mail to that domain would arrive unassigned.

**Back up the database.** Nothing does it for you (DEPLOY section 5): `dc exec -T postgres pg_dump -U inbox inbox | gzip > ~/inbox-backups/inbox-$(date +%F).sql.gz`, ideally nightly, and copy the files off the machine.

**Restore the database.** Stop the server (`dc stop inbox`), then
```bash
dc exec -T postgres psql -U inbox -d postgres -c 'drop database inbox' -c 'create database inbox owner inbox'
gunzip -c ~/inbox-backups/inbox-YYYY-MM-DD.sql.gz | dc exec -T postgres psql -U inbox inbox
dc up -d
```
(**unverified**; rehearse it once on a copy.) Objects in R2 are independent: the startup catch-up lists R2 and re-imports every raw message the restored database does not know, so mail received after the dump comes back by itself (read/archived state after the dump does not).

**The Mac mini rebooted or lost power.** Docker restarts the containers (`restart: unless-stopped`) once Docker itself is running (Docker set to start at login; sleep disabled, DEPLOY section 2). Check `dc ps` and that Tailscale is connected; the catch-up imports what arrived meanwhile. Mail still reaches Gmail throughout.

**Upgrade.** `dc pull && dc up -d` runs `inboxd migrate` first (goose migrations are forward-only and additive so far). If the new version misbehaves, set `INBOX_IMAGE` in `.env` to the previous tag or digest and `dc up -d`; the schema is backward compatible with the previous version unless a release note says otherwise.

## What runs when

| Job | Schedule | Purpose |
|---|---|---|
| `ingest_process` | on each new mail | parse, store, route, queue Gmail wants |
| Gmail write-back engine | polls every 2 s | labels/archive/read in Gmail; undo window 8 s; 6 tries over ~16 min |
| `reconcile_r2` | at start, every `RECONCILE_INTERVAL` (default 5 min), and on `POST /v1/mail:refresh` | list R2 and import raw mail Postgres lacks (a lost ping, or downtime); resumes after a saved position, full compare daily |
| `routing_check` | hourly + on demand | Cloudflare catch-all state per business |
| `health_snapshot` | every 5 min | store health, emit `health.changed`, ping `DEADMAN_URL` |
| `prune` | daily 03:00 UTC | apply `retention_days`, clean housekeeping tables |
| `thread_hydrate` | after a Gmail lookup | import your sent replies for the thread |
| `backfill_page` | while an import runs | pull older mail, ~10 messages/s, pauses on rate limits |

## Limits to know

- One server instance only (see DEPLOY.md). Live events and the Gmail engine are per process.
- Per-device API rate limit: 10 requests/s (burst 30). Login: lock per address after four wrong passwords (5 s doubling to 15 min) and about 1 attempt/s overall (a flood of wrong guesses can briefly block your own sign-in; devices already signed in are unaffected).
- Email Routing drops mail over 25 MiB before the Worker sees it.
- Gmail API: shared limiter 15 requests/s; `429`/rate-limit answers pause everything until `Retry-After`.
