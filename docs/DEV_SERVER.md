# Running the app against the real server (simulator)

```bash
make dev-server          # foreground, http://127.0.0.1:18090, Ctrl-C stops it
make dev-server-start    # detached (log: server/bin/dev-server.log); prints base URL + token; idempotent
make dev-server-stop     # stops the detached server (compose keeps running; nobody runs `make down`)
scripts/dev-server.sh status   # "running pid … URL" or "stopped" (exit 1)
make ios-e2e             # start, run the InboxLiveTests UI tests against it with the dev token, always stop
```

It starts Postgres + S3 (docker compose), migrates, loads demo data through the real ingest path (see *Demo data*), registers a
device with a **fixed, public dev token**, and runs `inboxd` in the foreground. Re-running is safe (demo data and the device are idempotent).
No Gmail or Cloudflare credentials are involved, so Gmail/onboarding-Cloudflare screens answer their "not configured" errors
(503 `gmail_not_configured`, 503 `cloudflare_not_configured`). For those flows use `make e2e` (fakes) instead.

| | value |
|---|---|
| Base URL | `http://127.0.0.1:18090` (override `PORT=...`) |
| Token | `dev-token-inbox-local-0000` (override `DEV_TOKEN=...`, min 16 chars), sent as `Authorization: Bearer <token>` |
| Login password | `dev-password-inbox-local` (override `DEV_PASSWORD=...`, min 12 chars): `POST /v1/auth/login` with `{"password", "device_name"}` returns a fresh device token |
| Ingest listener | `http://127.0.0.1:18091` (`INGEST_PORT`, default PORT+1): serves only `/ingest/v1/notify` |

Use `127.0.0.1`, not `localhost`: on some machines (OrbStack/Docker) another service already answers on `localhost:8080`, and `localhost` can resolve to IPv6 first.

## Demo data (`inboxd admin seed-demo`, idempotent)

3 businesses (Acme Studio `acme-demo.test`, Northwind Goods, Lumen Labs) with about 15 messages:

| Scenario | Where |
|---|---|
| unread / read mail, mail that needs a rule (unrouted) | most fixtures; Needs attention shows the unrouted ones |
| mail routed by a rule | rule "Demo orders" (`orders@` at Acme → label `Acme/Orders`); message "New order #1042" |
| conversation with a reply you sent | Acme, "March invoice": customer → **your sent reply** (direction `out`) → "Thanks!" |
| attachments | "Invoice attached" (PDF) and the inline-image fixture |
| remote images (blocked until allowed) | Northwind, "Your weekly offers" (2 remote images; `remote_image_count` 2) |
| HTML-only, multipart/alternative, non-UTF-8 and encoded-subject mail | the parse fixtures |
| failed items (Needs attention → Failed) | `store_failed` with no copy (Retry → 409 `needs_gmail`); `parse_failed` (Retry fails again, Dismiss); `store_failed` **with an intact copy** (Retry → 202 and the mail appears as "Retry me" at Northwind) |
| paused older-mail import | Acme: 340 of ~1200 scanned, paused by a Gmail rate limit for an hour (`pause_reason: rate_limited`) |

Re-running the seed (every start does) recreates a consumed failed item, so the retry scenario is repeatable after a restart.
Not available without real Google/Cloudflare: Gmail connect/labels/filters and the domain list answer 503 (`make e2e` covers them with fakes).

## Send test mail (`make send-test-mail`)

With `make dev-server-start` running, deliver a mail the way production does: the real Worker (`edge/e2e/harness.mjs`, needs Node 22.6+) forwards it, saves the raw mail to the local S3 store and sends the HMAC-signed notify. The script (`scripts/send-test-mail.mjs`) then waits until it shows up in `GET /v1/messages` and prints id, business, routed/unrouted and the matched rule. It starts and stops the harness itself.

```bash
make send-test-mail                                   # one plain mail to orders@acme-demo.test (matches rule "Demo orders" -> routed)
make send-test-mail SAMPLE=invoice                    # invoice | reply | unrouted | newsletter (scripts/samples/*.eml)
make send-test-mail TO=hello@northwind-demo.test FROM=a@b.test SUBJECT="Hi" BODY="Text" COUNT=3
make send-test-mail EML=path/to/mail.eml TO=orders@acme-demo.test
```

All options are optional: `TO` (envelope and header, default `orders@acme-demo.test`), `FROM`, `SUBJECT`, `BODY`, `EML`, `COUNT` (default 1), `SAMPLE`, plus `PORT` / `DEV_TOKEN` as for the dev server. Each send gets a fresh Message-ID, so repeats are not de-duplicated. Samples: `invoice` (PDF attachment), `reply` (plain), `newsletter` (remote images), all to `orders@` so they are routed by "Demo orders"; `unrouted` goes to an unknown domain (no business, needs a rule). A `TO` at a seeded business without a rule (e.g. `invoices@acme-demo.test`) lands in that business but unrouted. Exit code 1 if the server is not running or a mail does not appear within 30 s.

## Pointing the simulator app at it

The app already reads its base URL from the environment variable `INBOX_API_BASE_URL` (DECISIONS Q-T12-2). The proposed (debug builds only) addition:
`INBOX_DEV_TOKEN`: when set, the app stores that token in the Keychain at launch and skips the login screen (password sign-in, `POST /v1/auth/login`).

```bash
xcrun simctl launch --console booted com.casprine.inbox   # with the environment below set on the launching process:
SIMCTL_CHILD_INBOX_API_BASE_URL=http://127.0.0.1:18090 \
SIMCTL_CHILD_INBOX_DEV_TOKEN=dev-token-inbox-local-0000 \
xcrun simctl launch booted com.casprine.inbox
```

(`SIMCTL_CHILD_` is stripped by simctl and passed to the app.) In Xcode set the same two variables in the scheme (Run → Arguments → Environment Variables).
The simulator shares the Mac's network, so `127.0.0.1` reaches the server. App Transport Security must allow plain http to loopback
(`NSAllowsLocalNetworking`), which the Swift side owns.

## Handy

- The dev server uses its own database `inbox_dev`. Do not point it at database `inbox`: the integration tests migrate into throw-away schemas there and break if `inbox` already has tables in `public`.
- The compose services are shared by all sessions; nobody runs `make down` (it would stop the dev server's database). Tests and `make e2e` use their own throw-away schemas/databases, so they run fine next to the dev server.

- Live events: `GET /v1/events` with the same bearer token.
- Reset demo data: stop the server, `docker compose -f deploy/docker-compose.yml exec postgres psql -U inbox -d inbox -c 'DROP DATABASE inbox_dev'`, then start it again (the seed rebuilds everything).
- Add mail by hand like the Worker does: see `server/cmd/e2e` (`workerDelivers`) for the signed notify call (it goes to the ingest listener, port 18091, not the API port); `INGEST_SECRET` in dev is `dev-ingest-secret`.
- Catch-up: `POST /v1/mail:refresh` makes the server list the bucket and ingest any mail whose notify never arrived (also runs at startup and every `RECONCILE_INTERVAL`, default 5m).
