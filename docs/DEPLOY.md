# Deploying Inbox

What you (the owner) do, in order. The agents that built this never had access to your Cloudflare, Google, Tailscale or GitHub accounts
(DECISIONS D-007): everything below is untested against the real services and was checked only against local fakes. Anything marked
**unverified** is written from the tools' documentation or `--help` output and has not been run. Expect to adjust a screen name here or there.
Budget about two hours the first time.

The platform is your own Mac mini at home (D-019) running Docker, reached through Tailscale (D-020). Services in use (D-018): Cloudflare (Email
Routing, Email Worker, R2), Google OAuth for Gmail, GitHub (repository and Container Registry), Tailscale. Nothing else is assumed.

You need: the Mac mini with Docker (Docker Desktop or OrbStack) and Tailscale installed and signed in, a Cloudflare account with your domains,
a Google account (the Gmail you forward to), a GitHub account, `wrangler` (comes with `edge/`), Xcode, and `openssl`.

```
Mail to you@acme.com ──► Cloudflare Email Routing ──► Worker "inbox-worker" ─┬─► forwards to your Gmail (always first)
                                                                              ├─► saves raw mail in R2 bucket "inbox-mail"
                                                                              └─► signed POST to the Funnel address ─┐
                                                                                                                     ▼
 Mac mini (Docker compose)                                                                               tailscale funnel :8443
   inbox-server  :8081  only /ingest/v1/notify  ◄────────────────────────────────────────────────────────────────────┘
                 :8080  the app API             ◄── tailscale serve :443 (tailnet only) ◄── iPhone app (Tailscale on)
   postgres      (container, volume pgdata, no published port)
   inbox-server also talks out to: R2 (S3 API), Gmail API, Cloudflare API
```

## 0. Decide names

| Thing | Example | Notes |
|---|---|---|
| Mac mini tailnet name | `mini.tail1234.ts.net` | MagicDNS name of the Mac mini (HTTPS certificates and MagicDNS are already enabled) |
| API address | `https://mini.tail1234.ts.net` | Tailscale Serve on 443; becomes `PUBLIC_BASE_URL` (no trailing slash) |
| Ingest address | `https://mini.tail1234.ts.net:8443/ingest/v1/notify` | Tailscale Funnel on 8443; becomes the Worker's `INGEST_URL` |
| Worker | `inbox-worker` | must match `CF_WORKER_NAME` (default `inbox-worker`) and the onboarding screen |
| R2 bucket | `inbox-mail` | |
| iOS bundle id | `com.casprine.inbox` | from `ios/project.yml` |

Make a **staging** copy first if you can (a second Worker and R2 bucket, a spare domain, and a second compose project on another machine or with other ports) before switching a real domain.
The Worker never loses mail if the server is down: it forwards to Gmail first, and the server catches up from R2 (section 8, "Catching up from R2").

## 1. Secrets you generate

```bash
openssl rand -hex 32      # INGEST_SECRET  (the Worker and the server must share it)
openssl rand -base64 32   # the Gmail token encryption key, used as TOKEN_ENC_KEYS=k1:<value> (losing every key means reconnecting Gmail; rotation: RUNBOOK)
openssl rand -base64 24   # POSTGRES_PASSWORD (only used on the compose network)
```

And choose **OWNER_PASSWORD**: the one password you type in the app (D-012). At least 12 characters; the server refuses to start with anything shorter or with none.
It lives only in `deploy/.env` (and your password manager). The server hashes it in memory with argon2id and a random salt each time it starts; nothing about it is stored in the database,
so changing it is just editing `.env` and restarting (existing phones stay signed in, see RUNBOOK).

## 2. The Mac mini

1. Install Docker (Docker Desktop or OrbStack) and set it to **start at login**; containers use `restart: unless-stopped`, so they come back with Docker.
2. **Keep the Mac mini awake.** A sleeping Mac stops the server, the Tailscale node and the catch-up. System Settings → Energy: turn on *Prevent automatic sleeping when the display is off* and *Start up automatically after a power failure*
   (names vary by macOS version; **unverified**). From a terminal, `sudo pmset -a sleep 0 disksleep 0` does the same for the system sleep timers (**unverified**; check with `pmset -g`). Log in automatically (or keep a session open) if your Docker needs a user session.
   If it does go down, nothing is lost: mail is forwarded to Gmail and kept in R2, and the server imports it when it is back.
3. Tailscale: signed in on the Mac mini, with MagicDNS and HTTPS certificates enabled in the admin console (you already did this).

## 3. Cloudflare

All on the account that holds your domains.

1. **R2**: create the bucket `inbox-mail` (R2 → Create bucket). Create an R2 API token with *Object Read & Write* on that bucket and note
   the S3 values: `S3_ENDPOINT=https://<ACCOUNT_ID>.r2.cloudflarestorage.com`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_REGION=auto`.
   Keep the bucket private. Nothing but the Worker (binding `RAW`) and the server writes or lists it.
2. **API token for the server** (My Profile → API Tokens → Create): permissions *Zone → Zone → Read* and *Zone → Email Routing Rules → Read*,
   scoped to the zones you will use. The server only reads; it never edits your routing. → `CF_API_TOKEN`, and the account id → `CF_ACCOUNT_ID`.
3. **Email Routing**: for each domain: Email → Email Routing → enable it (Cloudflare adds the MX/TXT records), and add your Gmail
   as a verified destination address.
4. **Deploy the Worker** from `edge/` (after section 6 gives you the Funnel address):
   ```bash
   cd edge && npm ci
   npx wrangler login
   # edit wrangler.jsonc: FORWARD_TO = your Gmail, INGEST_URL = https://mini.tail1234.ts.net:8443/ingest/v1/notify
   npx wrangler secret put INGEST_SECRET        # paste the value from section 1
   npm test && npx wrangler deploy
   ```
   (`.github/workflows/deploy.yml` does the same from GitHub if you prefer; it deploys only the Worker.)
5. **Do not switch a domain yet.** Finish sections 4–8 first, then see "Switching a domain over" below.

## 4. Container image on GitHub

**Status: the workflows are written and linted (`actionlint`) but have never run.** There is no GitHub remote for this project and none was created;
nothing is published anywhere until you push the repository yourself.

`.github/workflows/image.yml` builds `deploy/Dockerfile` for `linux/amd64` and `linux/arm64` with buildx (the Mac mini is arm64 if it is Apple silicon) and publishes it to GitHub Container Registry as
`ghcr.io/<owner>/<repo>/inbox-server` (lower-cased). It smoke-tests the image (`inboxd --help`) before publishing and again on the pushed digest, attaches an SBOM and
max-mode provenance to the image, and records a signed build-provenance attestation. Layers are cached in the GitHub Actions cache.

| Trigger | Result |
|---|---|
| push to `main` | tags `main`, `latest`, `sha-<7 chars>` |
| tag `v1.2.3` | tags `1.2.3`, `1.2`, `sha-<7 chars>` |
| pull request touching `server/` or `deploy/` | build + smoke test only, nothing is pushed |

**First-time setup**
1. Create the GitHub repository and push. The first run of *Server image* creates the package; it needs no secrets (it uses the built-in `GITHUB_TOKEN`).
2. In the repository settings, Actions → General → Workflow permissions: allow read and write (needed for `packages: write` and attestations).
3. Package visibility (GitHub → your profile → Packages → `inbox-server` → Package settings):
   - **Public** (simplest): the image holds no secrets (all configuration is environment variables in `deploy/.env` on your Mac mini), so the Mac mini can pull it with no credentials.
   - **Private**: on the Mac mini run `docker login ghcr.io -u <your GitHub user>` once with a personal access token that has the `read:packages` scope (**unverified** details; see GitHub's docs for container registry login).

**Cut a release**
```bash
git tag v1.0.0 && git push origin v1.0.0     # builds and publishes 1.0.0, 1.0 and sha-…
```

**Verify an image you pulled**
```bash
gh attestation verify oci://ghcr.io/<owner>/<repo>/inbox-server:1.0.0 --repo <owner>/<repo>
docker buildx imagetools inspect ghcr.io/<owner>/<repo>/inbox-server:1.0.0     # platforms, SBOM, provenance
```

Until a first image exists you can build locally on the Mac mini instead: `docker build -f deploy/Dockerfile -t inbox-server .` and set `INBOX_IMAGE=inbox-server` in `deploy/.env`.

## 5. Run the stack (Docker compose)

`deploy/docker-compose.prod.yml` runs three things: `postgres` (a container beside the server, data in the named volume `pgdata`), `migrate` (one-shot `inboxd migrate`) and `inbox` (the server).
Nothing is published to the network: the server's two ports bind to `127.0.0.1` only, and Postgres has no published port.

```bash
cd deploy
cp .env.example .env && $EDITOR .env            # every value is explained in the file; .env is git-ignored
docker compose -f docker-compose.prod.yml pull
docker compose -f docker-compose.prod.yml up -d
docker compose -f docker-compose.prod.yml ps    # inbox must become "healthy"
curl -s http://127.0.0.1:8080/readyz            # {"status":"ready"}
```

The server **refuses to start** (the container restarts and logs why) when `OWNER_PASSWORD` is unset or shorter than 12 characters, or `RECONCILE_INTERVAL` is not a duration of at least 10 s.

Run exactly one `inbox` container: the job queue, the Gmail write-back engine and live events (SSE) live inside the process; a second copy would run every scheduled job twice and split app connections.
Optional: `DEADMAN_URL` (a healthchecks.io-style URL). The server pings it after every 5-minute health snapshot, so you hear about it if the server stops.

**Backups.** The database is the `pgdata` volume. Nothing backs it up for you. A nightly dump (put it in `crontab -e` or a `launchd` job; **unverified**, adjust paths):
```bash
cd /path/to/inbox/deploy && docker compose -f docker-compose.prod.yml exec -T postgres pg_dump -U inbox inbox | gzip > ~/inbox-backups/inbox-$(date +%F).sql.gz
```
Copy the dumps off the machine too (Time Machine, another disk). Raw mail is separate and already off-machine: it lives in R2. If you lose the database, restore the newest dump and the server re-adopts every raw message in R2 that the dump lacks (section 8).
Keep `deploy/.env` somewhere safe as well: `TOKEN_ENC_KEYS` is needed to read the stored Gmail token (or you simply reconnect Gmail).

**Update**
```bash
cd deploy && docker compose -f docker-compose.prod.yml pull && docker compose -f docker-compose.prod.yml up -d    # migrate runs first
```
Pin `INBOX_IMAGE` to a version tag or digest (`...:1.0.0`, `...@sha256:...`) for repeatable rollbacks; roll back by setting the previous value and running `up -d` again.
Do not run `docker compose down -v`: `-v` deletes the database volume.

## 6. Tailscale: publishing the two ports

Two ports, two audiences (D-020):

| Port on the Mac mini | Serves | Published with | Who can reach it |
|---|---|---|---|
| `127.0.0.1:8080` | the app API (`/v1/...`, `/oauth/google/callback`, `/mcp`, `/healthz`, `/readyz`) | **Tailscale Serve** on 443 | devices on your tailnet |
| `127.0.0.1:8081` | **only** `POST /ingest/v1/notify` (HMAC + timestamp checked) | **Tailscale Funnel** on 8443 | the public internet (the Cloudflare Worker) |

```bash
# API, tailnet only. Flags checked against `tailscale serve --help` (Tailscale 1.104.2), not run against your tailnet:
tailscale serve --bg --https=443 http://127.0.0.1:8080
# Ingest, public. Flags checked against `tailscale funnel --help`; the allowed public ports (443, 8443, 10000) are from Tailscale's docs, UNVERIFIED here:
tailscale funnel --bg --https=8443 http://127.0.0.1:8081
tailscale serve status        # shows both; `tailscale funnel status` shows what is public
```

(`tailscale serve --bg 8080` and `tailscale funnel --bg --https=8443 8081` are the short forms of the same thing.)

**Funnel needs the `funnel` node attribute in your tailnet policy file** (admin console → Access controls). Without it `tailscale funnel` refuses or shows a link to enable it. The usual form is
```json
"nodeAttrs": [ { "target": ["autogroup:member"], "attr": ["funnel"] } ]
```
(**unverified**; check Tailscale's Funnel documentation for the current syntax, or let the admin console add it when `tailscale funnel` prompts you). You can limit `target` to the Mac mini's tag or your user.

What Funnel exposes: only the ingest listener, which answers `404` for every path except `POST /ingest/v1/notify` and `401` for anything not correctly signed with `INGEST_SECRET` within 5 minutes.
The login, the API and the Gmail callback are on the other listener and are not reachable from the internet. The Worker's `INGEST_URL` is the Funnel address (`https://mini.tail1234.ts.net:8443/ingest/v1/notify`).

**Login rate limiting behind Serve.** Serve forwards from the Mac mini itself, so the server sees every request coming from the Docker gateway. The login limiter then has one "client" for everyone (the global cap and the
lockout still protect you; a wrong-password burst also locks you out). If Serve passes the caller's address in `X-Forwarded-For` (**unverified**; check with a request and `docker compose logs`), set `LOGIN_IP_HEADER=X-Forwarded-For` in `.env`
so the lockout is per phone. Never set it unless every request really passes through that proxy: a client could otherwise choose its own "address".

The phone needs the Tailscale app, signed in to the same tailnet and connected, to reach `https://mini.tail1234.ts.net`.

## 7. Google OAuth (Gmail connection)

Needed for write-back (labels/archive), importing filters, older mail and retrying failed mail. Without it Inbox still receives and forwards mail.

1. console.cloud.google.com → new project "Inbox" → **APIs & Services → Library → Gmail API → Enable**.
2. **OAuth consent screen**: User type *External*; app name "Inbox", your email as support/developer contact. Add the scope
   `https://www.googleapis.com/auth/gmail.modify`. **Publish the app ("In production")**: while it is in *Testing*, Google expires the refresh token after
   7 days and Inbox will keep asking you to reconnect. You will see an "unverified app" warning once when you connect; that is expected for personal use
   (restricted scopes need Google's security review only above 100 users).
3. **Credentials → Create credentials → OAuth client ID → Web application**. Authorised redirect URI exactly the Serve address plus the callback path:
   `https://mini.tail1234.ts.net/oauth/google/callback` (this is `PUBLIC_BASE_URL` + `/oauth/google/callback`). A `.ts.net` redirect is known to work from the owner's Calendar setup in another project (D-020). Copy the client id and secret.
   The consent screen opens in the phone's browser, so the phone must be on the tailnet while you connect Gmail.
4. Put `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` in `deploy/.env` and run `docker compose -f docker-compose.prod.yml up -d`.
5. **Open question (DECISIONS Q-T18-2):** the server asks only for `gmail.modify`. If Gmail's *Bring your filters* step fails with a "scope missing"
   message, Google wants `gmail.settings.basic` as well: add it to the consent screen and to `Scopes` in `server/internal/gmail/gmail.go`
   (`Config.scopes()`), rebuild the image, and reconnect Gmail.

## 8. Catching up from R2

R2 is the queue (D-020; there is no Cloudflare D1). The Worker saves every mail as `raw/YYYY/MM/<ulid>.eml` and then pings the server. A ping can be lost (server down, Mac asleep, Funnel or network trouble), so the server
does not rely on it: it **lists R2 and ingests everything Postgres does not know yet**

- once at startup,
- every `RECONCILE_INTERVAL` (default `5m`; a Go duration such as `90s`, `10m`, `1h`; minimum `10s`),
- and when the app asks (`POST /v1/mail:refresh`, e.g. pull to refresh).

To keep this cheap it remembers a position: the last key below which everything has been seen (stored in the `settings` table as `reconcile_cursor`), and only lists after it. The position only moves past objects older than one hour, and once a day
(and whenever the position is missing) it lists and compares the whole bucket instead, so nothing that sorted oddly stays missed. Objects younger than a minute (`RECONCILE_MIN_AGE`, default `1m`) are left for their normal ping.
The same mail arriving by ping and by catch-up is ingested once. An outage of any length is covered: when the Mac mini comes back, the startup run imports everything that arrived in the meantime.

## 9. The app

On first launch the app asks for the **server address** and the **password**: the Serve address (`https://mini.tail1234.ts.net`) and `OWNER_PASSWORD`. It calls `POST /v1/auth/login`, gets a device token and keeps it in the Keychain; the password itself is not kept.
Debug builds can instead set `INBOX_API_BASE_URL` in the scheme's environment. An `https` URL needs no App Transport Security exception. Build with `make ios-build`; install to your phone from Xcode.
Revoke a lost phone from Settings → Devices (or `DELETE /v1/devices/{id}`).

## Switching a domain over

Do this one domain at a time, and watch the app's Health screen after each.

1. In the app: onboarding / Settings → Businesses → add the domain. The domain list shows each domain's routing state.
2. Cloudflare → the domain → Email → Email Routing → **Routing rules → Catch-all address**: action *Send to a Worker*, destination **inbox-worker**, enabled.
3. Back in the app, tap *Check again*; the state turns **ok**. Send yourself a test mail to `anything@acme.com`: it should arrive in Gmail *and* in Inbox within seconds.
4. **Rollback** (any time, one click): set the catch-all back to *Send to an email* → your Gmail. Mail keeps flowing to Gmail; Inbox shows "sends straight to email again".

## Environments

| | local development | production (Mac mini) |
|---|---|---|
| Database | docker compose Postgres (`make up`, `deploy/docker-compose.yml`) | Postgres container, volume `pgdata` (`deploy/docker-compose.prod.yml`) |
| Object storage | SeaweedFS in the dev compose | Cloudflare R2 bucket `inbox-mail` |
| Server | `make dev-server` (docs/DEV_SERVER.md) | container from `ghcr.io/<owner>/<repo>/inbox-server` |
| Worker | `edge/e2e/harness.mjs` | `inbox-worker` |
| Google OAuth | fakes in tests | one client |

CI (`.github/workflows/ci.yml`) runs on every push: server vet + gofmt + all tests against Postgres/S3 from docker compose + the OpenAPI contract, the end-to-end run, the edge tests and
type-check, and the iOS build/tests. The server image is built by `image.yml` (section 4). `deploy.yml` is manual (*Actions → Deploy → Run workflow*) and deploys only the Worker; the server is updated on the Mac mini (section 5).
Create GitHub environments `staging` and `production` with `CLOUDFLARE_API_TOKEN` as a secret and `WORKER_NAME`, `INGEST_URL`, `FORWARD_TO`, `CLOUDFLARE_ACCOUNT_ID` as variables; add required reviewers on `production` if you want an approval step.

## Every setting

Set in `deploy/.env` (see `deploy/.env.example`). Missing optional ones switch that feature off. The server refuses to start only for the first row and for a malformed `RECONCILE_INTERVAL`.

| Variable | Needed for | Notes |
|---|---|---|
| `OWNER_PASSWORD` | signing in | **required**, at least 12 characters; hashed in memory (argon2id), never stored |
| `DATABASE_URL` | everything | compose builds it from `POSTGRES_PASSWORD` |
| `HTTP_ADDR` | | the API listener, default `:8080` |
| `INGEST_ADDR` | receiving mail | the ingest-only listener (only `/ingest/v1/notify`), default `:8081`; starts only when `INGEST_SECRET` and S3 are set |
| `PUBLIC_BASE_URL` | OAuth redirect, Alfather `mcp_url` | the Serve address, no trailing path |
| `INGEST_SECRET` | receiving mail | shared with the Worker; without it the ingest listener does not start |
| `S3_ENDPOINT` `S3_BUCKET` `S3_ACCESS_KEY` `S3_SECRET_KEY` `S3_REGION` | storing mail, the job queue | R2; region `auto`. Without them ingest and every job are off |
| `RECONCILE_INTERVAL` | catching up from R2 | default `5m`, minimum `10s` |
| `RECONCILE_MIN_AGE` | catching up from R2 | default `1m`; objects younger than this wait for their ping |
| `LOGIN_IP_HEADER` | login lockout per client | optional, e.g. `X-Forwarded-For`; only behind a proxy you trust (section 6) |
| `TOKEN_ENC_KEYS` | Gmail | `id:base64,id:base64` (32 random bytes each, base64). The first key seals new data, all keys open old data; ids are letters, digits, `-`, `_` (e.g. `k1:…`, later `k2:…,k1:…`) |
| `TOKEN_ENC_KEY` | Gmail | older single-key form (id `k1`); still accepted. If both are set the list wins and this key stays readable as `k1` |
| `GOOGLE_CLIENT_ID` `GOOGLE_CLIENT_SECRET` | Gmail | |
| `CF_API_TOKEN` `CF_ACCOUNT_ID` | domain list and routing checks | read-only token |
| `CF_WORKER_NAME` | routing checks | default `inbox-worker` |
| `DEADMAN_URL` | | pinged every 5 minutes |
| `GOOGLE_AUTH_URL` `GOOGLE_TOKEN_URL` `GOOGLE_REVOKE_URL` `GMAIL_API_BASE` `CF_API_BASE` | tests / local fakes only | never set in production |

Removed with the move to password sign-in: `APPLE_AUDIENCE`, `OWNER_APPLE_SUB`, `BOOTSTRAP_CODE` and `inboxd admin bootstrap` (they are ignored if still set).

Worker settings (`edge/wrangler.jsonc`, `wrangler secret put`): `FORWARD_TO` (your Gmail), `INGEST_URL` (the Funnel address), `INGEST_SECRET`, R2 binding `RAW` → `inbox-mail`.

## Still unverified (do these early)

- **Tailscale commands and policy** (section 6): the flags match `--help` on Tailscale 1.104.2, but nothing was run against your tailnet; the `funnel` node attribute syntax and the allowed Funnel ports come from documentation.
- **Does Serve send the caller's address** in `X-Forwarded-For`? (section 6, login lockout per client.)
- **Does `message.raw` stay readable after `forward()`?** (Q-T02-2) The Worker forwards first, then reads the raw stream. If the real runtime refuses, the
  failure path records `store_failed` and mail is still delivered; switch to buffering first if it does. Test with the first mail on staging.
- **Does `gmail.modify` cover listing filters?** (Q-T18-2, section 7 step 5.)
- **Can the Cloudflare Worker reach a Funnel address?** Funnel serves a normal public HTTPS certificate on `*.ts.net`, so it should; confirm with the first real mail (the Worker log shows the notify status).
- **Email Routing messages over 25 MiB** are rejected by Cloudflare itself, so the Worker never sees them.
- **The Mac mini staying awake** (section 2) and Docker coming back after a reboot.
