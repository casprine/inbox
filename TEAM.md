# Inbox — Team Split and Working Agreement

Repo: `/Users/casprine/Desktop/vendor/personal/inbox` (not a git repo; no git ops). Coordinator: session **cordinator [9ab2af]** (message it with SendMessage `to: "cordinator"`).

Read first, in this order: `MILESTONES.md` (task cards + shared conventions), `DECISIONS.md` (decided items are binding), `ENGINEERING_PLAN.md` (architecture), then your role section below.

## Roles and ownership

| Role | Session | Owns (only edit these) |
|---|---|---|
| **Backend** | `backend agent: inbox` | `server/`, `edge/`, `api/openapi.yaml`, `deploy/`, `Makefile` (server targets), `docs/RUNBOOK.md`, `docs/DEPLOY.md`, `scripts/` |
| **Swift** | `swift agent: inbox` | `ios/` (incl. `ios/docs/` screenshots), iOS Makefile targets |
| **Design** | `design agent: inbox` | `docs/design-ref/`, `DESIGN_QA.md`, `docs/design-specs/`, the design canvas artifact (https://claude.ai/artifact/TZo3ijDTzFYA46Pp3D4LQL), design-token spec |
| **Coordinator** | `cordinator` | `MILESTONES.md` statuses, `DECISIONS.md` consolidation, `TEAM.md` |

Cross-role needs go through SendMessage to the owning role, copy the coordinator. Never edit another role's files; ask them. Shared append-only files: `DECISIONS.md` (append under "Open questions" as `Q-<role>-<n>`, one short paragraph each, with your default).

## Task assignment (task ids = cards in MILESTONES.md)

**Done and verified (T01–T16):** repo skeleton, edge worker, schema/store, blob/crypto, MIME parse, notify+ingest job, auth, businesses/messages API, search+SSE+attention, OpenAPI contract test, iOS project+design system, API/persistence, Mail, Message, Search features, rules engine + API.

**Backend queue (in order):** T18 Gmail OAuth/account → T19 write-back engine (+undo window, retry endpoint `POST /v1/messages/{id}/gmail:retry`) → T20 labels/filters import/thread hydrate (`GET/POST /v1/gmail/labels`, `GET /v1/gmail/filters`, `POST /v1/rules:import`) → T22 Cloudflare + businesses + health → T25 backfill → T27 retention/prune/prefs (`/v1/prefs`, `DELETE /v1/data`) → T31 Alfather MCP server → T28 deploy/CI/runbook → T29 e2e script (server side). Every route must be in `api/openapi.yaml` (contract test enforces). **Publish each finished endpoint group to the Swift agent** (one message: routes + example JSON) so it can swap mocks for real calls. Also: `gmail_id`, `gmail_state`, attempts, labels must be in `GET /v1/messages/{id}` (Swift needs them for Details/Gmail-not-found).

**Swift queue (in order):** T17 Rules + Attention → *Design fix pass (see DESIGN_QA.md, blocking before new screens ship)* → T21 Gmail → T23 Onboarding → T24 Businesses/Settings/Health → T26 Import screens → T27 (iOS Privacy/prefs) → T31 (iOS Alfather pairing) → T29 snapshot + UI tests. Work against `api/openapi.yaml` + mock mode (`-UseMockData`); wire to live server endpoints as the backend agent publishes them.

**Design queue:**
1. **D1 (do first, ~30 min): design-fidelity audit** of what Swift has built (T11–T15: design system gallery, Businesses, Business page, Message, thread, images-blocked, Search; screenshots in `ios/docs/*.png`; code in `ios/InboxKit/Sources/`) against the canvas boards (`docs/design-ref/*.dc.html` exports, or the live canvas). Write `DESIGN_QA.md`: one row per defect — board id, what the design shows, what the app does, severity (P0 wrong/broken, P1 visible mismatch, P2 polish), exact fix (token/spacing/typography values). Send the Swift agent the P0/P1 list.
2. **D2: close design gaps** with real boards or written specs in `docs/design-specs/` (and canvas if feasible): sign-in step for Sign in with Apple (OB01 "Get started"); text-accent contrast fix (accent #F83D34 on cream ≈ 3:1 — specify a text variant e.g. ≈#C8281F, plus dark); privacy copy ("index (D1)" → "on your Inbox server"); NA05 "31 MB over the 25 MB limit" example copy; Alfather pairing copy for the connect-out model (DECISIONS D-006); any state the Swift agent reports as undesigned.
3. **D3: gate**: for every Swift deliverable that lands (screenshots in `ios/docs/`), compare to the board and reply "pass" or a defect list within the same session turn. Don't let screens ship with P0/P1 open.

## Communication rules (owner priority: stop message noise)

- **Silence is the default.** Do not message the coordinator or peers for progress, task completion, pass/fail on routine work, acknowledgements, or "idle/done" notices. Write results to files instead: status in `MILESTONES.md`/`STATUS.md` (coordinator), findings in `DESIGN_QA.md`, `docs/STATE*.md`, `DECISIONS.md`.
- **Message only when blocked** on another role, or for a P0 defect, or for one final hand-off when your whole queue is done. One message, ≤40 words, no replies to it, no thanks.
- Never ask for or send idle-notifications, never reply to a message that does not ask a question.
- Do not subscribe to other sessions (no notify_when_idle).
- Handoffs between roles go in a file the other role reads (e.g. `docs/handoff/<from>-to-<to>.md`), not in chat.

## Working agreement

- Work in small verified steps; each task ends with its verification command run for real (`make test-integration`, `make ios-test`, `go vet`, `gofmt`). Say so honestly if something could not be run.
- Keep the repo green: do not leave failing builds/tests. If another role's change breaks you, message that role.
- curl is blocked in this environment (D-009); use Go/Node/Swift clients.
- No real third-party accounts or secrets (D-007); local fakes only.
- After each task: do not message anyone; record the result in your own status file (see Communication rules).
- Decisions you can take safely: take them, log in DECISIONS.md. Stop and ask only for things that need the owner (money, accounts, external services, destructive actions).
- The owner is away; the goal is that everything in MILESTONES.md is finished and verified when they return. Keep going down your queue without waiting for replies unless blocked.
