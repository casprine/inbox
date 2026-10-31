# Inbox runbook

Operations after day zero (the first deployment is `docs/DEPLOY.md`). Everything the server knows about its own health is on one screen in the app (Settings → Health) and in `GET /v1/health`; start there, then use the matching section.
Commands assume the Mac mini, run from the repository root, with
`alias dc='docker compose -f deploy/docker-compose.prod.yml'` (the compose file reads `deploy/.env`).

Golden rule: **mail is never lost while the Worker runs.** It forwards to Gmail before doing anything else, so the worst case is "Inbox is behind", not "mail is missing".

## Quick commands

```bash
dc ps                                       # postgres + inbox must be running; inbox "healthy"
dc logs -f inbox                            # JSON lines, no passwords, tokens, subjects, addresses or bodies
dc restart inbox                            # safe: jobs are durable and leased; also clears the login lockout memory
curl -s http://127.0.0.1:8080/readyz        # {"status":"ready"}   or 503 {"status":"unavailable","failed":"database"|"blob store"}
dc exec inbox inboxd admin rotate-keys      # see "Rotate the Gmail token encryption key"
scripts/backup.sh                           # a backup right now
tailscale serve status; tailscale funnel status   # what is published (API on 443 for the tailnet, ingest on 8443 for the internet)
```

Liveness from a device on the tailnet: open `https://mini.tail1234.ts.net/readyz`. `/healthz` only says the process is up; `/readyz` also checks Postgres and the R2 bucket (the Docker health check uses it; the R2 answer is cached for 30 seconds).
Local development equivalents: `make up`, `make test-integration`, `make prod-smoke`; nobody runs `make down` on the shared dev stack.

## Backups

**What is where.** Two stores hold your data:

| | holds | lost if it is lost |
|---|---|---|
| **R2** `inbox-mail` (Cloudflare) | every raw mail as `raw/YYYY/MM/<ulid>.eml` | the original mail (Gmail still has the forwarded copy) |
| **Postgres** (`pgdata` volume on the Mac mini) | the message index, businesses (domains), rules, devices, preferences, the Gmail connection, read/archive state, job queue | see below |

**If Postgres is lost** and you restore nothing: the server starts empty and its catch-up (`reconcile_r2`) re-imports every raw message from R2, so the **message index is rebuilt** (subjects, senders, bodies, attachments). These **cannot be rebuilt** from R2 and must be re-done by hand:
businesses/domains, rules (also in Gmail if you imported them from filters), devices (every phone signs in again), the Gmail connection (reconnect; the token is sealed in the database), preferences and retention settings, read/archived/starred state, Gmail sync state and labels per message,
backfill history. Re-imported mail has no business until you re-add the domains (and then needs re-sorting, **unverified**). A dump taken last night restores all of that except what changed since.
`deploy/.env` (especially `TOKEN_ENC_KEYS`) is **not** in the dump: keep it safe separately, without the key a restored Gmail token cannot be read and Gmail is simply reconnected.

**Take a backup** (safe while the server runs):
```bash
scripts/backup.sh [DIR]      # DIR or BACKUP_DIR in deploy/.env, default ~/inbox-backups
```
It runs `pg_dump --format=custom` inside the Postgres container, checks that the dump can be listed (`pg_restore --list`), writes `inbox-<UTC timestamp>.dump` (mode 600) atomically, deletes all but the newest `BACKUP_KEEP` (default 14), and, only when all four `BACKUP_S3_*` variables are set, copies it to that bucket with the `aws` CLI.
Use a second R2 bucket for the off-site copy, never `inbox-mail`. Dumps contain every message body and your Gmail token (sealed with `TOKEN_ENC_KEYS`): treat them like the database.

**Schedule (launchd).** `deploy/launchd/com.inbox.backup.plist.example` runs it at 03:30 daily; its header has the install steps (`launchctl bootstrap`, test with `launchctl kickstart -k`). Log: `~/Library/Logs/inbox-backup.log`.
Check it ran: `ls -lt ~/inbox-backups | head`. A LaunchAgent only runs while the user is logged in (automatic login), and after a sleep it runs on wake. The plist is linted; loading it on a Mac mini is **unverified**.

**Rehearse a restore** (do this once now, and after changing Postgres versions):
```bash
scripts/restore.sh ~/inbox-backups/inbox-<timestamp>.dump --into rehearsal     # a NEW database beside the live one; refuses if it exists
dc exec -T postgres psql -U inbox -d rehearsal -c 'select (select count(*) from businesses) b, (select count(*) from messages) m, (select count(*) from devices) d'
dc exec -T postgres psql -U inbox -d postgres -c 'drop database rehearsal'
```

**Restore for real** (Postgres lost or damaged, or a rollback; see "Upgrade and roll back"):
```bash
scripts/backup.sh                                              # if the database still answers: keep what you have first
scripts/restore.sh ~/inbox-backups/inbox-<timestamp>.dump --replace          # prints what it would do
scripts/restore.sh ~/inbox-backups/inbox-<timestamp>.dump --replace --yes    # stops inbox, drops and recreates the database, restores, starts inbox
```
When the server starts it applies any newer migrations and re-imports from R2 the mail that arrived after the dump. If Postgres itself is gone (volume deleted): `dc up -d postgres`, wait until healthy, then the same `--replace --yes` command (it creates the database).
A full-machine loss: reinstall Docker, restore `deploy/.env` and the newest dump, `dc up -d postgres`, restore, `dc up -d --wait`.

## Upgrade and roll back

The image comes from GitHub Container Registry (`image.yml` publishes tags `X.Y.Z`, `X.Y`, `sha-<7>`, `main`, `latest` and prints the digest in the run summary). Compose uses exactly what `INBOX_IMAGE` says; there is no default and no `:latest`.

**Upgrade**
```bash
scripts/backup.sh                                   # 1. the pre-upgrade backup; note the file name it prints
$EDITOR deploy/.env                                 # 2. INBOX_IMAGE=ghcr.io/<owner>/<repo>/inbox-server:<new version>  (or @sha256:<digest>)
dc pull inbox                                       # 3. (private package: docker login ghcr.io was done once, see DEPLOY step 3)
dc up -d --wait                                     # 4. recreates inbox; it applies new migrations before listening
curl -s http://127.0.0.1:8080/readyz; dc logs --tail 30 inbox      # 5. ready; "applying migrations" then "listening"
```
Migrations are **forward-only** and run under a Postgres advisory lock at every start, so a start with nothing new changes nothing. The release notes say if a version changes the schema in a way the previous version cannot read.
Postgres itself stays on `postgres:16.6-alpine`; a major-version change needs a dump and restore into a new volume, planned separately.

**Roll back**
1. *The new version misbehaves but no migration was involved* (or release notes say the previous version still reads the schema): set `INBOX_IMAGE` back to the previous tag or digest, `dc up -d --wait`. Done.
2. *A migration ran and the previous version cannot use the new schema* (it fails at start or errors on queries): restore the pre-upgrade backup under the old image.
   ```bash
   $EDITOR deploy/.env                                                        # INBOX_IMAGE = the previous tag/digest FIRST
   scripts/restore.sh ~/inbox-backups/<pre-upgrade file>.dump --replace --yes # drops the migrated database, restores the old schema, starts the old image
   ```
   What you lose: changes made in the app between the backup and the rollback (rules, read state, new devices). Mail is not lost: it is in Gmail and R2, and the startup catch-up re-imports it.
3. Keep the previous image tag in your notes; `docker image ls` on the Mac mini also still has it until pruned.

## The Mac mini offline or asleep

Nothing is lost: the Worker forwards to Gmail and stores the raw mail in R2 whether or not the server is reachable; when the server is back its startup run and `RECONCILE_INTERVAL` sweeps import everything that arrived (pings that failed are simply ignored).
The app shows an offline or "can't reach server" state and cached mail meanwhile.

Prevent it (macOS, **unverified** flags; compare with `man pmset`):
```bash
sudo pmset -a sleep 0 disksleep 0     # never sleep (display may still sleep)
sudo pmset -a autorestart 1           # start up after a power failure
sudo pmset -a womp 1                  # wake for network access
pmset -g                              # current settings;  pmset -g log | grep -iE "sleep|wake" | tail   # what happened
```
Also: automatic login for the Docker user, Docker set to start at login, and a `DEADMAN_URL` (healthchecks.io style) so you are told when the server stops pinging. After a power cut or reboot check `dc ps`, `tailscale status`, `/readyz`.
`caffeinate -s` is a stopgap, not a fix.

## Tailscale, Serve and Funnel

```bash
tailscale status                       # the Mac mini is connected; MagicDNS name matches PUBLIC_BASE_URL
tailscale serve status                 # https://mini.tail1234.ts.net (443) -> 127.0.0.1:8080  (tailnet only)
tailscale funnel status                # https://mini.tail1234.ts.net:8443  -> 127.0.0.1:8081  (public)
```
Funnel and Serve config persists across restarts when created with `--bg`; if either list is empty after a reboot, run the two commands from DEPLOY step 6 again. From a device **outside** the tailnet:
`curl -si https://mini.tail1234.ts.net:8443/healthz | head -1` must be `404` (reachable and serving nothing but the signed notify), `POST .../ingest/v1/notify` with a bad signature must be `401`, and `https://mini.tail1234.ts.net/readyz` must **not** answer.
A hang on 8443 means Funnel is off, the `funnel` policy attribute is missing, or the Mac mini is offline. `404` on the API address from the tailnet means Serve points at the wrong port.
Test the signed path by hand (the same signature the Worker computes):
```bash
S=$(grep ^INGEST_SECRET deploy/.env | cut -d= -f2); TS=$(date +%s); B='{"key":"raw/2026/01/none.eml","env_from":"a@b.c","env_to":"x@y.z"}'
SIG=$(printf '%s.%s' "$TS" "$B" | openssl dgst -sha256 -hmac "$S" | sed 's/^.* //')
curl -si -X POST https://mini.tail1234.ts.net:8443/ingest/v1/notify -H "X-Inbox-Timestamp: $TS" -H "X-Inbox-Signature: $SIG" -d "$B" | head -1     # 202
```

## Change the password, rotate keys, revoke a device

- **Change the sign-in password.** Edit `OWNER_PASSWORD` in `deploy/.env` (at least 12 characters, not a placeholder), then `dc up -d --wait`. Phones already signed in keep working (they hold device tokens, not the password). To make every phone sign in again, revoke all devices:
  `dc exec postgres psql -U inbox inbox -c "update devices set revoked_at = now() where revoked_at is null"`.
- **Revoke a device** (a lost phone): Settings → Devices → revoke (`DELETE /v1/devices/{id}`), or `dc exec postgres psql -U inbox inbox -c "select id, name, last_seen_at from devices where revoked_at is null"` then `update devices set revoked_at = now() where id = '<id>'`. The phone's next request is `401` and it returns to sign-in.
- **Rotate `INGEST_SECRET`.** `openssl rand -hex 32`; run `cd edge && npx wrangler secret put INGEST_SECRET` and set the same value in `deploy/.env` then `dc up -d --wait`, close together. In between the Worker's pings fail with 401 and the next reconcile run (within `RECONCILE_INTERVAL`) recovers the mail.
- **Rotate the Gmail token encryption key** (no downtime, no reconnecting).
  1. `openssl rand -base64 32`. Put the new key **first** and keep the old one: `TOKEN_ENC_KEYS=k2:<new>,k1:<old>` in `deploy/.env`, `dc up -d --wait` (a single `TOKEN_ENC_KEY` counts as `k1`).
  2. Re-seal: `dc exec inbox inboxd admin rotate-keys` → `current key "k2": re-sealed 1, already current 0`. Safe to repeat.
  3. Only now drop the old key: `TOKEN_ENC_KEYS=k2:<new>`, `dc up -d --wait`. Remove a key too early and the server says which key id the stored token needs; add it back. If a key is truly lost, reconnect Gmail in the app.
- **Change the Postgres password.** Not by editing `.env` alone (the volume keeps the old one): `dc exec postgres psql -U inbox -c "alter user inbox password '<new>'"`, then set `POSTGRES_PASSWORD` in `.env` and `dc up -d --wait`.
- **Rotate the R2 keys.** Create a new token in Cloudflare, put it in `.env`, `dc up -d --wait`, check `/readyz`, then delete the old token.

## Logs in OpenTelemetry

Set `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_INGEST_KEY` and `OTEL_LOG_LEVEL` in `deploy/.env` and run `up -d`. Logs keep going to `docker logs` as before; the same lines (at or above the level) are also sent in batches. If the endpoint is wrong the server logs `telemetry disabled` and keeps running; in production a non-https endpoint stops startup. Nothing secret is in the log lines, so none is sent.

## Log lines (what each means)

Logs are JSON, one object per line (`dc logs inbox`). They never contain passwords, device tokens, the ingest secret, Gmail tokens or codes, or mail subjects, addresses and bodies (a test, `TestServeLogsNeverContainSecrets`, and `make prod-smoke` check this).

| `msg` | Level | Meaning and action |
|---|---|---|
| `applying migrations`, then `listening` (once per listener, `ingest_only` true for the second) | info | normal start |
| `shutting down: draining requests`, then `stopped` | info | graceful stop after `docker stop`/`dc restart` (SIGTERM); requests get 20 s to finish |
| `invalid configuration` + `problem` | error | the server refused to start; each line names a variable; fix `deploy/.env` (DEPLOY step 2) |
| `fatal` | error | the process exits and Docker restarts it; the `err` says why (for example `migrations:` the database is unreachable or a migration failed) |
| `INGEST_SECRET not set: the ingest listener is off` / `S3 not configured: ingest disabled` | warn | development only; in production the server would have refused to start |
| `Gmail not configured (…)` | warn | `GOOGLE_CLIENT_ID`/`SECRET`/`TOKEN_ENC_KEYS` missing: normal until step 8 of DEPLOY |
| `token encryption keys invalid: Gmail disabled` | warn | `TOKEN_ENC_KEYS` is malformed |
| `gmail api error` / `gmail request failed` | warn | Gmail answered with an error or could not be reached; see Health → Gmail below |
| `enqueue thread_hydrate`, `shutdown` | warn | a job could not be queued / a listener did not drain in time; rarely matters |

Docker health: `dc ps` shows `healthy` once `/readyz` passes; `unhealthy` for a minute after a restart is normal, `unhealthy` for longer means `curl -s http://127.0.0.1:8080/readyz` and read its `failed` field (database or blob store).

## Running the Worker spike (Q-T02-2)

The question (D-016): can the Email Worker read `message.raw` after it has already forwarded the mail? Run it once, on a spare domain, before any real domain is switched. Full steps are in `docs/DEPLOY.md` step 7. In short:
`cd edge && npx wrangler tail`, send a mail with an attachment to the spare domain, and check that (a) it reached Gmail, (b) `tail` shows no `store failed`, (c) R2 has `raw/YYYY/MM/<id>.eml` of about the mail's size, (d) the app shows it.
Failure means buffering the raw message before `forward()` in `edge/src/index.ts`; mail still reaches Gmail meanwhile, and the app lists the item under *Needs attention → Failed* as `store_failed`.

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

**Switch a domain back to plain forwarding.** Cloudflare → catch-all → *Send to an email*. Inbox shows the routing problem for that business until you remove it or switch back.

**Delete all stored mail.** Settings → Privacy → Delete stored mail (needs the typed confirmation). Gmail itself is untouched. Or set a retention period (nightly prune at 03:00 UTC).

**Remove a business.** Settings → Businesses → Remove. Its mail, attachments and raw copies are deleted; Cloudflare is not changed, so new mail to that domain would arrive unassigned.

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
