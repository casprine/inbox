# Inbox: where things stand (for the owner)

Last updated 2026-10-31. Code, docs and tests are finished for a production deploy on your Mac mini. What is left needs your accounts. Every decision I took is in `COORDINATOR_LOG.md` (C-14 to C-45) and `DECISIONS.md` (D-011 to D-020); review those first.

## Verified (final checker run, 2026-10-31, sandbox stack only)
| Check | Result |
|---|---|
| `make lint` | clean |
| `make test-integration` | 22 Go packages pass |
| `make e2e` (real server and Worker, fakes for Google/Cloudflare) | 47 checks pass |
| `make edge-test` | 8 pass, tsc clean |
| `make docker-build` | pass |
| `make prod-smoke` (prod compose, own project, throwaway secrets) | 59 pass |
| `make edge-dryrun` (`wrangler deploy --dry-run`) | pass |
| `make ios-test` | 300 pass |
| `make ios-ui-test` | 10 pass |
| `make ios-e2e` (live dev server) | 11 pass |
| actionlint / shellcheck | actionlint clean; shellcheck 1 warning (SC2155) and 7 info notes, nothing blocking |
| Design gate 12 (`docs/design-audit/GATE12.md`) | P0 0, P1 0 |

## What now exists
- **Sign-in:** server address + password (`OWNER_PASSWORD`), rate-limited, revocable device tokens, signed-out state on a rejected token. No Apple sign-in.
- **Production server:** strict startup checks (`INBOX_ENV=production`), `/healthz`, `/readyz`, self-migrating, graceful shutdown, ingest on its own listener for Tailscale Funnel, API for Tailscale Serve, R2 catch-up sweep (`RECONCILE_INTERVAL`), `POST /v1/mail:refresh`.
- **Deploy kit:** `deploy/docker-compose.prod.yml` (Postgres beside the server), `deploy/.env.example`, `scripts/backup.sh`, `scripts/restore.sh`, launchd plist example, image CI to GHCR, upgrade and rollback steps. `docs/DEPLOY.md` is one day-zero checklist; `docs/RUNBOOK.md` covers operations.
- **App:** Open Runde fonts, red-text and type rules applied, OB07/OB12 added, snapshots for every audited screen (light, dark, AX3 where needed).

## What only you can do (day zero, in `docs/DEPLOY.md` order)
1. Create the GitHub repo, push, tag `v0.1.0` (image CI has never run), and make the package public or log the Mac mini in to ghcr.io.
2. Cloudflare: R2 bucket and token, API token, Email Routing per domain, verified destination address, deploy the Worker with `wrangler secret put INGEST_SECRET`.
3. Tailscale: add the `funnel` attribute to your policy, run `tailscale serve` (443 to the API) and `tailscale funnel` (8443 to the ingest port).
4. Google: OAuth client with `gmail.modify` and `gmail.settings.basic`, published "In production", redirect on your `.ts.net` host.
5. Fill `deploy/.env`, start the stack, keep the Mac mini awake (`pmset`), schedule the backup, and rehearse one restore.
6. Run the Worker spike (can `message.raw` be read after `forward()`? Q-T02-2) on one spare domain before switching domains over one at a time.
7. Apple Developer account to install the app on your phone (no longer used for sign-in).

## Not verified (cannot be, without your accounts or a device)
Real Cloudflare (Email Routing, R2, Worker reaching your Funnel address), Google consent with both scopes, Tailscale Serve/Funnel and whether Serve forwards the caller address (otherwise all devices share one login lockout; set `LOGIN_IP_HEADER`), GHCR and GitHub Actions, the `prod-smoke` CI job on a Linux runner, launchd and `pmset` on the Mac mini, a physical iPhone, VoiceOver speech, OB07 live (the sandbox has no Google config), swipe actions, pull-to-refresh, QuickLook preview, empty Rules, keyboard-up states.

## Decisions still open for you
1. **Alfather** (D-014, C-22): how does it really connect? The assumed contract stands.
2. **Is Gmail required?** (C-31) In the sandbox `setup_step` is always `gmail`, so "I already set this up" lands on the Gmail step. If Gmail is optional, change the rule to "done means businesses exist".
3. Review the 21 design decisions (`docs/design-specs/D6-gate12-decisions.md`) and the choices in `COORDINATOR_LOG.md`; any can be reversed.

## Known gaps
Polish left out on purpose: NA06 retrying detail, ML08 chip case, ML10 banner dot, GM06 radio glyph at AX3, pull-to-refresh tint, OB12 tracking. A deleted message whose blob purge failed can come back at the next daily R2 scan. Single Gmail account and single Alfather. No push notifications (by design). Boards were compared as text/CSS, never as rendered pictures. Snapshot baselines are valid only on our own simulator. `DESIGN_QA.md` keeps one historical block (marked).
