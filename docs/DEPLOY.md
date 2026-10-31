# Deploying Inbox: day-zero checklist

One linear list, in the order to do it. Each step says which account it needs, the exact commands, and **Verify** lines to pass before moving on.
Operations after day zero (backups, upgrades, rotation, troubleshooting) are in `docs/RUNBOOK.md`.

**How far this has been checked.** The agents that built this never had access to your Cloudflare, Google, Tailscale or GitHub accounts (D-007). What *was* run:
the server image, `deploy/docker-compose.prod.yml`, the backup and restore scripts and the whole mail flow, locally, with `make prod-smoke` (SeaweedFS stands in for R2;
fakes stand in for Cloudflare and Google). Everything that touches your real accounts is written from the tools' documentation or `--help` and is marked **unverified**. Expect to
adjust a screen name. Budget two to three hours the first time.

The platform is your own Mac mini at home (D-019) running Docker, reached through Tailscale (D-020). Services (D-018): Cloudflare (Email Routing, Email Worker, R2), Google OAuth for
Gmail, GitHub (repository and Container Registry), Tailscale. Nothing else is assumed.

```
Mail to you@acme.com ──► Cloudflare Email Routing ──► Worker "inbox-worker" ─┬─► forwards to your Gmail (always first)
                                                                              ├─► saves raw mail in R2 bucket "inbox-mail"
                                                                              └─► signed POST to the Funnel address ─┐
                                                                                                                     ▼
 Mac mini (Docker compose)                                                                               tailscale funnel :8443
   inbox   :8081  only /ingest/v1/notify  ◄──────────────────────────────────────────────────────────────────────────┘
           :8080  the app API             ◄── tailscale serve :443 (tailnet only) ◄── iPhone app (Tailscale on)
   postgres (container, named volume pgdata, no published port)
   inbox also talks out to: R2 (S3 API), Gmail API, Cloudflare API
```

## Which step needs which account

| Step | Needs | Where you work |
|---|---|---|
| 1 Mac mini | the Mac mini | terminal |
| 2 Names and secrets | nothing | terminal |
| 3 Image | GitHub | github.com, terminal |
| 4 R2 and Cloudflare token | Cloudflare | dash.cloudflare.com |
| 5 Start the stack | the Mac mini (+ GitHub login if the package is private) | terminal |
| 6 Publish ports | Tailscale | terminal, admin console |
| 7 Worker | Cloudflare | terminal (`wrangler`) |
| 8 Google OAuth | Google | console.cloud.google.com |
| 9 App, first domain, Gmail | all of them | the iPhone |
| 10 Email Routing per domain | Cloudflare | dash.cloudflare.com |
| 11 Backups | the Mac mini | terminal |
| 12 Acceptance | all | |

Do not skip ahead: steps 7 and 10 send real mail to the server, so 5 and 6 must be verified first.

---

## 1. The Mac mini

1. Install Docker (Docker Desktop or OrbStack). Set it to **start at login**. The containers use `restart: unless-stopped`, so they come back with Docker. Turn on automatic login for the user that runs Docker.
2. Install Tailscale, signed in to your tailnet, with MagicDNS and HTTPS certificates enabled (admin console → DNS). You already did this (D-020).
3. Keep it awake and bring it back after a power cut (see RUNBOOK "Mac mini offline or asleep" for the meaning of each flag):
   ```bash
   sudo pmset -a sleep 0 disksleep 0 autorestart 1 womp 1
   pmset -g          # shows sleep 0, autorestart 1
   ```
4. Install `jq`, `openssl` (present on macOS), `awscli` only if you want the off-site backup copy (step 11), and `git`.
5. Clone the repository: `git clone <your repo> ~/inbox && cd ~/inbox` (or copy the folder; the Mac mini needs only `deploy/` and `scripts/`).

**Verify:** `docker run --rm hello-world` works; `tailscale status` shows the Mac mini connected; `pmset -g | grep -E ' sleep|autorestart'`.

## 2. Names and secrets (no account)

Decide the names (example values):

| Thing | Example | Becomes |
|---|---|---|
| Mac mini tailnet name | `mini.tail1234.ts.net` | |
| API address (Tailscale Serve, 443) | `https://mini.tail1234.ts.net` | `PUBLIC_BASE_URL` (no trailing slash) |
| Ingest address (Tailscale Funnel, 8443) | `https://mini.tail1234.ts.net:8443/ingest/v1/notify` | the Worker's `INGEST_URL` |
| Worker | `inbox-worker` | `CF_WORKER_NAME` (default) |
| R2 bucket | `inbox-mail` | `S3_BUCKET` |

Create `deploy/.env` and generate the secrets:

```bash
cd ~/inbox/deploy
cp .env.example .env && chmod 600 .env
echo "POSTGRES_PASSWORD=$(openssl rand -hex 24)"     # paste into .env
echo "INGEST_SECRET=$(openssl rand -hex 32)"          # paste into .env; the Worker gets the same value in step 7
echo "TOKEN_ENC_KEYS=k1:$(openssl rand -base64 32)"   # paste into .env; back it up (RUNBOOK "Backups")
```

Also set `OWNER_PASSWORD` (the one password you type in the app: at least 12 characters, your own, never a placeholder) and `PUBLIC_BASE_URL`. Keep `.env` in your password manager as well.
Every variable is explained in `.env.example`. **The server refuses to start** and logs every missing or weak value by name (one line per problem) when: `OWNER_PASSWORD` is missing, short or a placeholder;
`INGEST_SECRET` is shorter than 32 characters; `TOKEN_ENC_KEYS` is missing or not valid `id:base64` of 32 bytes; `PUBLIC_BASE_URL` is not `https://`; the Postgres password in `DATABASE_URL` is shorter than 16 characters;
any `S3_*` is empty; only one of `GOOGLE_CLIENT_ID`/`GOOGLE_CLIENT_SECRET` is set; or `RECONCILE_INTERVAL` is not a duration of at least `10s`.

**Verify:** `grep -c '=$' .env` lists the variables still empty; at this point only the `S3_*` ones (step 4), `INBOX_IMAGE` (step 3) and the optional ones should be.

## 3. Container image (GitHub)

Status: `.github/workflows/image.yml` and `ci.yml` are written and pass `actionlint`, but **have never run on GitHub**: there is no remote for this project and none was created.

1. Create a GitHub repository and push this project to it (`git remote add origin …; git push -u origin main`). Settings → Actions → General → Workflow permissions: *Read and write*.
2. The push to `main` runs **Server image**: builds `deploy/Dockerfile` for `linux/amd64` and `linux/arm64`, smoke-tests it (starts, runs as non-root, refuses to start with no settings), pushes, and writes the exact reference to the run's summary.
3. Cut the release you will deploy: `git tag v0.1.0 && git push origin v0.1.0` builds tags `0.1.0`, `0.1`, `sha-<7>`. The image name is `ghcr.io/<owner>/<repo>/inbox-server` (lower case).
4. **Package visibility** (GitHub → your profile → Packages → `inbox-server` → Package settings):
   - *Public* is simplest: the image holds no secrets (configuration is environment variables in `deploy/.env`), so the Mac mini pulls with no login.
   - *Private* (the default for a private repository): on the Mac mini create a **classic** personal access token with only the `read:packages` scope (GitHub → Settings → Developer settings → Tokens (classic); fine-grained tokens did not support packages when this was written, **unverified**), then
     ```bash
     echo "$PAT" | docker login ghcr.io -u <your GitHub user> --password-stdin     # stored in the macOS keychain by Docker
     ```
5. Set the image in `deploy/.env`: a version tag, or best the digest from the run summary, which cannot move:
   ```
   INBOX_IMAGE=ghcr.io/<owner>/<repo>/inbox-server:0.1.0
   INBOX_IMAGE=ghcr.io/<owner>/<repo>/inbox-server@sha256:<digest>      # alternative
   ```
   There is deliberately no default, and `:latest` is not used for the server image or for Postgres (`postgres:16.6-alpine`).

**Verify:** `docker pull "$(grep ^INBOX_IMAGE deploy/.env | cut -d= -f2)"` succeeds; `gh attestation verify oci://ghcr.io/<owner>/<repo>/inbox-server:0.1.0 --repo <owner>/<repo>` (optional, needs `gh`) checks the build provenance.
Until a first image exists you can build on the Mac mini: `docker build -f deploy/Dockerfile -t inbox-server:local .` and set `INBOX_IMAGE=inbox-server:local`.

## 4. Cloudflare: R2 and an API token

1. **R2** (dashboard → R2 → Create bucket): `inbox-mail`, keep it private. R2 → *Manage R2 API tokens* → create a token with *Object Read & Write* on that bucket only. Put into `.env`:
   `S3_ENDPOINT=https://<ACCOUNT_ID>.r2.cloudflarestorage.com`, `S3_ACCESS_KEY`, `S3_SECRET_KEY` (and `S3_BUCKET=inbox-mail`, `S3_REGION=auto`). The Worker uses a binding, not these keys.
2. **API token for the server** (My Profile → API Tokens → Create): *Zone → Zone → Read* and *Zone → Email Routing Rules → Read* on the zones you will use. The server only reads (domain list and routing checks); it never edits your routing.
   → `CF_API_TOKEN`; the account id (dashboard sidebar) → `CF_ACCOUNT_ID`. Optional: without them the domain picker is off and you type domains by hand. Whether "Email Routing Rules: Read" alone covers the routing-state call is **unverified**; if the app's *Check again* reports a permission error, widen the token (for example add *Zone → Zone Settings → Read*) and retry.
3. **Email Routing destination**: Email → Email Routing → *Destination addresses* → add your Gmail and click the verification link Cloudflare mails to it. The Worker's `FORWARD_TO` must be a verified destination of the account, or `forward()` fails.

**Verify:** the bucket exists and is empty; the token works against it (done for you in step 5: `/readyz` reports `ready` only when the server can reach the bucket with these keys).

## 5. Start the stack (the Mac mini)

```bash
cd ~/inbox/deploy
docker compose -f docker-compose.prod.yml pull
docker compose -f docker-compose.prod.yml up -d --wait
docker compose -f docker-compose.prod.yml ps
```

Compose runs `postgres` (container beside the server, data in the named volume `pgdata`, no published port) and `inbox` (the server). The server applies its database migrations itself at every start,
under a Postgres advisory lock (forward-only, safe to repeat). Both ports are published on `127.0.0.1` only. The containers run read-only with all capabilities dropped, as a non-root user, with log rotation.
Run exactly **one** `inbox` container: the job queue, the Gmail engine and live events live inside the process.

If `up` fails, `docker compose -f docker-compose.prod.yml logs inbox` lists every setting that is wrong, one line each (`"msg":"invalid configuration","problem":"INGEST_SECRET is …"`). Fix `.env`, run `up -d --wait` again.

**Verify:**
```bash
curl -s http://127.0.0.1:8080/healthz     # {"status":"ok"}
curl -s http://127.0.0.1:8080/readyz      # {"status":"ready"}   503 {"status":"unavailable","failed":"database"|"blob store"} otherwise
docker compose -f docker-compose.prod.yml ps          # inbox: healthy
docker compose -f docker-compose.prod.yml logs inbox | grep -E 'listening|applying migrations'
```
`failed: blob store` means R2 refuses the keys or the endpoint is wrong. Never run `docker compose down -v` here: `-v` deletes the database.

## 6. Tailscale: publish the two ports

Two ports, two audiences (D-020): the API is for your phone (tailnet only); the ingest listener is for Cloudflare (public, signed requests only).

```bash
tailscale serve --bg --https=443 http://127.0.0.1:8080       # the app API, tailnet only
tailscale funnel --bg --https=8443 http://127.0.0.1:8081     # ONLY the ingest listener, public internet
tailscale serve status; tailscale funnel status
```
Flags match `--help` of Tailscale 1.104.2; the commands were not run against your tailnet (**unverified**). Funnel's allowed public ports (443, 8443, 10000) are from Tailscale's documentation (**unverified**).
**Funnel needs the `funnel` node attribute** in your tailnet policy (admin console → Access controls), usually `"nodeAttrs": [{"target": ["autogroup:member"], "attr": ["funnel"]}]` (**unverified**; the CLI prints a link to enable it when it is missing).

What Funnel exposes: the ingest listener answers `404` for every path except `POST /ingest/v1/notify`, and `401` unless the request is signed with `INGEST_SECRET` within 5 minutes. Login, the API and the Gmail callback are on the other listener and never public (`make prod-smoke` checks this).

**Verify** (this is the first thing that touches Tailscale for real):
```bash
curl -s https://mini.tail1234.ts.net/readyz                  # from a device on the tailnet (the phone's browser works): {"status":"ready"}
# from OUTSIDE the tailnet (phone on mobile data with Tailscale off):
curl -si https://mini.tail1234.ts.net:8443/healthz | head -1  # HTTP/2 404  (reachable, serves nothing else)
curl -si -X POST https://mini.tail1234.ts.net:8443/ingest/v1/notify -d '{}' | head -1   # HTTP/2 401
curl -s -m 5 https://mini.tail1234.ts.net/readyz              # must NOT answer from outside the tailnet (timeout)
```
Login limiter behind Serve: the server sees every request from one address. If Serve forwards the caller's address in `X-Forwarded-For` (**unverified**), set `LOGIN_IP_HEADER=X-Forwarded-For` in `.env` and `up -d` so the lockout is per phone. Never set it unless every request really passes through that proxy.

## 7. The Cloudflare Worker

Config: `edge/wrangler.jsonc`. R2 binding `RAW` → `inbox-mail`; vars `FORWARD_TO` (your Gmail) and `INGEST_URL` (the Funnel address); one secret, `INGEST_SECRET`. No other secrets are needed.

```bash
cd ~/inbox/edge && npm ci
$EDITOR wrangler.jsonc        # FORWARD_TO = your verified Gmail; INGEST_URL = https://mini.tail1234.ts.net:8443/ingest/v1/notify
npm test && npx tsc --noEmit
npx wrangler deploy --dry-run --outdir /tmp/worker-check      # bundles and validates the config offline; uploads nothing (run for you: passes)
npx wrangler login
npx wrangler secret put INGEST_SECRET                         # paste the value from deploy/.env
npx wrangler deploy
npx wrangler secret list                                      # shows INGEST_SECRET
```
(`.github/workflows/deploy.yml` does the deploy from GitHub: it needs a `CLOUDFLARE_API_TOKEN` secret with *Workers Scripts: Edit*, and `WORKER_NAME`, `INGEST_URL`, `FORWARD_TO`, `CLOUDFLARE_ACCOUNT_ID` variables per environment. The local route above is simpler for one person.)
Wrangler 3.x is pinned in `edge/package-lock.json`; the dry run works with it. A newer major exists; upgrade deliberately, not during setup.

**Verify:** the Worker exists (dashboard → Workers & Pages → `inbox-worker`) and `npx wrangler tail` connects. Do **not** point a domain at it yet.

### The Worker spike (Q-T02-2): do it before any real domain

The open question (D-016): can the Worker still read `message.raw` after `message.forward()`? The Worker forwards first and stores second. Test on **one spare domain** (or a throwaway subdomain), with the real Worker:

1. Do step 10 for the spare domain only.
2. In one terminal: `cd edge && npx wrangler tail`. Send a mail with an attachment to `spike@<spare domain>` from another account.
3. **Good:** the mail arrives in Gmail, and the tail shows no `store failed`. In the dashboard (R2 → `inbox-mail`) an object `raw/YYYY/MM/<id>.eml` exists with a size close to the mail's size, and the mail appears in the app within seconds.
4. **Bad:** the tail shows `store failed` (or the object is 0 bytes), the app's *Needs attention → Failed* lists `store_failed`. Gmail still got the mail. Then change `edge/src/index.ts` to read the raw message into memory **before** calling `forward()`
   (send the buffered bytes to R2 afterwards), re-run `npm test`, redeploy, repeat. Tell the coordinator; this is the only code change the spike can cause.
5. Also check a mail near 20 MiB (Email Routing rejects over 25 MiB before the Worker, nothing to do) and a mail with no `Message-ID`.

## 8. Google OAuth (Gmail connection)

Needed for labels/archive write-back, importing filters, older mail and retries. Without it Inbox still receives and forwards mail.

1. console.cloud.google.com → new project "Inbox" → *APIs & Services → Library → Gmail API → Enable*.
2. *OAuth consent screen*: User type *External*; app name "Inbox", your email as support and developer contact. **Add both scopes** (the server asks for both in one consent, D-015):
   `https://www.googleapis.com/auth/gmail.modify` and `https://www.googleapis.com/auth/gmail.settings.basic`. **Publish the app ("In production")**: in *Testing*, Google expires the refresh token after 7 days.
   You will see an "unverified app" warning once when you connect; that is expected for personal use.
3. *Credentials → Create credentials → OAuth client ID → Web application*. Authorised redirect URI exactly `https://mini.tail1234.ts.net/oauth/google/callback` (`PUBLIC_BASE_URL` + `/oauth/google/callback`). A `.ts.net` redirect is known to work from your Calendar setup (D-020).
4. Put `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` in `deploy/.env`; `cd ~/inbox/deploy && docker compose -f docker-compose.prod.yml up -d --wait`.

**Verify:** `docker compose -f docker-compose.prod.yml logs inbox | grep "Gmail not configured"` prints nothing. The connect screen's Google page lists both permissions (read, compose and manage mail; manage basic settings and filters). The connection itself is tested in step 9.

## 9. The app, the first domain and Gmail

1. Build and install the app (`make ios-build`; install to the phone from Xcode, docs/RUN_ON_DEVICE.md). The phone needs Tailscale on and connected.
2. First launch: server address `https://mini.tail1234.ts.net`, password `OWNER_PASSWORD`. The server answers with a device token and a `setup_step` (`domains`, `gmail` or `done`), so signing in again later resumes where setup stopped.
3. Add your first (spare) domain in the app (onboarding → Domains). Then connect Gmail. The phone opens Google's consent page; the redirect returns to the app.

**Verify:** the app shows the domain with routing state *unknown* (not yet switched); Settings → Health has no red rows except routing for the new domain; Gmail shows connected as your address.

## 10. Email Routing, per domain

Repeat for each domain, one at a time, spare domain first. Labels are from Cloudflare's current dashboard and may differ (**unverified**).

1. dash.cloudflare.com → the domain → **Email → Email Routing**. If it is not enabled, click *Get started* / *Enable Email Routing* and accept the records Cloudflare proposes (it adds the MX and SPF TXT records). Delete any old MX records for the domain first; Email Routing needs its own MX records only.
2. *Destination addresses*: your Gmail must show **Verified** (step 4.3).
3. **Routing rules → Catch-all address → Edit**: Action **Send to a Worker**, Destination **inbox-worker**, Status **Enabled**, Save. (The Worker must already be deployed, step 7, or it is not in the list.)
4. In the app: Businesses → the domain → *Check again*. The state becomes **ok** (the server reads the catch-all via the Cloudflare API). Without `CF_API_TOKEN` this check is off; test with a mail instead.
5. Send a test mail to `anything@<domain>` from another account.

**Verify:** the mail is in Gmail **and** in the app within seconds; `docker compose -f docker-compose.prod.yml logs inbox` shows no error; `npx wrangler tail` shows the Worker run without `store failed` or `notify gave up`.

**Rollback for a domain (any time, one click):** the same catch-all → Action **Send to an email** → your Gmail. Mail keeps flowing to Gmail; the app shows "sends straight to email again".
Do the spike (step 7) with the spare domain, then switch the real domains one at a time and watch Settings → Health after each.

## 11. Backups

The database is the only thing here that cannot be rebuilt from elsewhere. Take the first backup now and schedule the rest (RUNBOOK "Backups" says what a dump holds and what is lost without it).

```bash
cd ~/inbox
scripts/backup.sh                         # writes ~/inbox-backups/inbox-<UTC timestamp>.dump (custom format), keeps the newest 14
scripts/restore.sh ~/inbox-backups/inbox-<timestamp>.dump --into rehearsal     # rehearsal: restore beside the live database; the server is untouched
docker compose -f deploy/docker-compose.prod.yml exec -T postgres psql -U inbox -d rehearsal -c 'select count(*) from businesses'
docker compose -f deploy/docker-compose.prod.yml exec -T postgres psql -U inbox -d postgres -c 'drop database rehearsal'
```
Schedule it nightly with launchd: copy `deploy/launchd/com.inbox.backup.plist.example` to `~/Library/LaunchAgents/com.inbox.backup.plist`, replace `/Users/YOU/inbox`, then
`launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.inbox.backup.plist` and test with `launchctl kickstart -k gui/$(id -u)/com.inbox.backup` (the plist was linted with `plutil`; loading it on a Mac mini is **unverified**).
Copy `~/inbox-backups` and `deploy/.env` off the machine (Time Machine, another disk) or set the four `BACKUP_S3_*` variables for an automatic copy to a **second** R2 bucket (not `inbox-mail`).

## 12. Final acceptance

Tick all of these before you call it live:

- [ ] `curl -s http://127.0.0.1:8080/readyz` is `ready`; `docker compose … ps` shows both containers up, `inbox` healthy.
- [ ] Reboot the Mac mini: everything comes back without you (Docker at login, `tailscale serve status` still lists both, `/readyz` ready).
- [ ] A test mail to each domain arrives in Gmail and in the app. Stop the server (`docker compose … stop inbox`), send another, start it: the mail appears in the app within `RECONCILE_INTERVAL` (catch-up from R2).
- [ ] Wrong password → "Incorrect password"; right password signs in; a revoked device is signed out (RUNBOOK "Revoke a device").
- [ ] Gmail: archive a message in the app and see it archived in Gmail.
- [ ] A backup exists, a rehearsal restore worked, the launchd job ran once.
- [ ] Settings → Health is all green. Optional: `DEADMAN_URL` set to a dead-man's-switch URL so you hear if the Mac mini goes dark.

---

## Every setting

Set in `deploy/.env` (`deploy/.env.example` has comments). In production mode (the image default) the server refuses to start when a required one is missing or weak.

| Variable | Needed for | Notes |
|---|---|---|
| `INBOX_IMAGE` | compose | version tag or digest of the server image; no default |
| `POSTGRES_PASSWORD` | compose | at least 16 characters; the server builds `DATABASE_URL` from it |
| `OWNER_PASSWORD` | signing in | **required**, at least 12 characters, not a placeholder; hashed in memory (argon2id), never stored |
| `PUBLIC_BASE_URL` | OAuth redirect, Alfather `mcp_url` | **required**, `https://`, no trailing slash |
| `INGEST_SECRET` | receiving mail | **required**, at least 32 characters, shared with the Worker |
| `TOKEN_ENC_KEYS` | Gmail token | **required**, `id:base64,id:base64` (32 random bytes each). The first key seals, all open. `TOKEN_ENC_KEY` (single, id `k1`) is the older form, still accepted |
| `S3_ENDPOINT` `S3_BUCKET` `S3_ACCESS_KEY` `S3_SECRET_KEY` `S3_REGION` | storing mail, the job queue | **required** in production; R2, region `auto` |
| `GOOGLE_CLIENT_ID` `GOOGLE_CLIENT_SECRET` | Gmail | both or neither |
| `CF_API_TOKEN` `CF_ACCOUNT_ID` `CF_WORKER_NAME` | domain list, routing checks | optional, read-only token; worker name default `inbox-worker` |
| `DEADMAN_URL` | | optional, pinged every 5 minutes |
| `RECONCILE_INTERVAL` `RECONCILE_MIN_AGE` | catch-up from R2 | defaults `5m` (minimum `10s`) and `1m` |
| `LOGIN_IP_HEADER` | per-client login lockout | optional, only behind a proxy you trust |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | base URL of your OpenTelemetry (OTLP/HTTP) backend; logs are sent to `<url>/v1/logs`. Empty = no telemetry. https required in production | optional |
| `OTEL_INGEST_KEY` | your backend's ingest key, sent as `Authorization: Bearer <key>` | optional |
| `OTEL_LOG_LEVEL` | lowest level sent: `error`, `warn`, `info` (default) or `debug`. Stdout is unchanged | optional |
| `OTEL_EXPORTER_OTLP_HEADERS` | extra headers `name=value,...` if your vendor does not use Bearer | optional |
| `API_BIND` `API_PORT` `INGEST_BIND` `INGEST_PORT` | compose | host side of the published ports; default loopback, 8080 and 8081 |
| `HTTP_ADDR` `INGEST_ADDR` | | container-side listen addresses (`:8080`, `:8081`); leave alone |
| `INBOX_ENV` | | `production` in the image and compose; leave alone |
| `BACKUP_DIR` `BACKUP_KEEP` `BACKUP_S3_*` | `scripts/backup.sh` | read from `.env` by the script, not by the containers |
| `GOOGLE_AUTH_URL` `GOOGLE_TOKEN_URL` `GOOGLE_REVOKE_URL` `GMAIL_API_BASE` `CF_API_BASE` | tests only | never set in production |

Removed with the move to password sign-in: `APPLE_AUDIENCE`, `OWNER_APPLE_SUB`, `BOOTSTRAP_CODE` and `inboxd admin bootstrap` (ignored if still set).

## Catching up from R2 (why a lost ping is harmless)

R2 is the queue (D-020; there is no Cloudflare D1). The Worker saves every mail as `raw/YYYY/MM/<ulid>.eml` and then pings the server. A ping can be lost (server down, Mac asleep, Funnel trouble), so the server lists R2 and
ingests everything Postgres does not know yet: at startup, every `RECONCILE_INTERVAL`, and when the app asks (`POST /v1/mail:refresh`, pull to refresh). It remembers a position so listing stays cheap and does a full comparison once a day.
Objects younger than `RECONCILE_MIN_AGE` are left for their normal ping. The same mail arriving by ping and by catch-up is ingested once.

## What was verified, and what was not

Verified locally (`make prod-smoke`: 59 checks; `make e2e`: 47 checks): the image builds and runs as non-root; it refuses weak configuration naming every variable; `/healthz`, `/readyz` (database and blob store); sign-in right and wrong; device tokens;
signed notify on the ingest port; the API port does not serve the ingest route and the ingest port serves nothing else; a lost ping recovered by the reconcile sweep; the refresh endpoint; backup, rotation and restore (into a new database and in place);
graceful stop (exit 0) and restart keeping data; no password, token, secret or mail body in the logs; the Worker bundles and validates (`wrangler deploy --dry-run`) and its tests pass against a fake.

**Unverified (do these early, in this order):**
1. **Funnel and Serve commands, the `funnel` policy attribute and the public port list** (step 6).
2. **`message.raw` readable after `forward()`**: the Worker spike (step 7, Q-T02-2).
3. **Can the real Worker reach the Funnel address?** It should (public HTTPS certificate); confirm with the first real mail.
4. **Does Serve send `X-Forwarded-For`?** (step 6).
5. **Google consent with both scopes**, and that Google keeps the refresh token valid once the app is "In production" (step 8).
6. **The Cloudflare token's permissions** are enough for the routing check (step 4.2), and the exact dashboard labels in step 10.
7. **GHCR**: the workflows have never run on GitHub; package visibility and the classic-token login (step 3).
8. **launchd job and `pmset` flags on your macOS version** (steps 1 and 11).
9. **Email Routing drops mail over 25 MiB** before the Worker; nothing to do, but it means such mail never reaches Inbox (Cloudflare bounces it).
