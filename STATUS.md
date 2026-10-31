# Inbox: where things stand (for the owner)

Last updated 2026-10-31. Phase 1 (31 cards) and Phase 2 are done. Round 3 (2026-10-31) applied your decisions D-011 to D-020: password sign-in instead of Sign in with Apple, Open Runde fonts, #BD251D for red text, Mac mini + Docker + Tailscale hosting, R2 catch-up. Nothing is committed to git, and nothing has touched a real Cloudflare, Google, Tailscale or GitHub account (D-007).

## Verified (checker run, 2026-10-31)
| Check | Result |
|---|---|
| `make lint` | clean |
| `make test-integration` | 20 Go packages pass |
| `make e2e` (real server and Worker, fakes for Google/Cloudflare) | 47 checks pass |
| `make edge-test` | 8 pass |
| `make docker-build` | pass |
| `make ios-test` | 182 pass |
| `make ios-ui-test` | 6 pass (after the worker fixed a state-leak flake; two clean runs) |
| `make ios-e2e` (live dev server) | 10 pass (not re-run after the last UI-test fix, which only touched mock mode) |

Not exercised: GitHub Actions, real Cloudflare (Worker, R2, Email Routing), Google sign-in, Tailscale Serve/Funnel, a physical device, VoiceOver speech, the Mac mini, Alfather on your LAN.

## What changed in round 3
- **Sign-in:** `POST /v1/auth/login` with `OWNER_PASSWORD` (plain env var, hashed in memory). Rate limiting with lockout. App has a server address + password screen. Apple sign-in removed.
- **Reachability:** ingest webhook on its own listener (`INGEST_ADDR`, :8081) for Tailscale Funnel; app API behind Tailscale Serve.
- **Catch-up:** server lists R2 on startup, every `RECONCILE_INTERVAL`, and on `POST /v1/mail:refresh`.
- **Fonts:** Open Runde bundled (OFL); mono is the system monospaced font.
- **Docs:** `docs/DEPLOY.md` and `docs/RUNBOOK.md` rewritten for the Mac mini. Fly deploy removed. Decisions are in `DECISIONS.md` (D-011..D-020).

## Decisions I still need from you
1. **Alfather** (D-014): how does it really connect? The assumed contract (it connects out to the server over MCP, the phone only pairs) stands until you check.
2. **Red fills:** #C8281F for fills that carry text (4.58:1), #F83D34 for textless fills; or one red with dark button text.
3. **"I already set this up"** lands on Mail if the server has businesses, else the domains step. Ask backend to return a setup step for exact resume?
4. **Signed-out state:** built but nothing opens it. Add 401 handling so a revoked device shows it?
5. **Open Runde credit:** one line in Settings → About, or show the full OFL text?
6. **Shared lockout:** if Tailscale Serve does not pass the caller address, all devices share one login lockout. Check `X-Forwarded-For` on the Mac mini and set `LOGIN_IP_HEADER` if so.

## What you have to do to go live
Follow `docs/DEPLOY.md`: Cloudflare (R2, Worker, Email Routing per domain), Google OAuth client (production status, redirect on the `.ts.net` host), `funnel` nodeAttr in the tailnet policy, `tailscale serve` and `tailscale funnel` for the two ports, `deploy/docker-compose.prod.yml` with `deploy/.env.example` filled in, keep the Mac mini awake, then run the Worker spike (can `message.raw` be read after `forward()`? Q-T02-2) on one spare domain before switching domains over one at a time. An Apple Developer account is still needed to install the app on your phone.

## Known gaps
About 15 minor design polish items (`DESIGN_QA.md`), Gate 10 B (Open Runde size/tracking table) and the red-text audit not done, `gmail.settings.basic` scope asked for but unverified, a deleted message whose blob purge failed can come back at the next daily R2 scan, single Gmail account and single Alfather, no push notifications (by design). `TEAM.md` and parts of `DESIGN_QA.md` still contain historical Apple/font wording.
