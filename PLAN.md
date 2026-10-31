# Inbox — End-to-End Plan (v3: Worker-first)

**Context:** several domains on Cloudflare use Email Routing to forward to your Gmail. There's no backend yet. Sorting happens today through Gmail filters.

**Decision:** put a Cloudflare **Email Worker** in front of Gmail. It sees every message first: it forwards to Gmail immediately, stores a copy, and processes it in the background. Gmail is used only as the place you *read* mail. The Gmail API is used for just two things: **writing back** (label, archive, insert digest) and a **one-time backfill** of old mail.

No Google Pub/Sub, no `watch` renewal, no history syncing. One Worker is the whole backend.

---

## 1. The experience

> A client emails `invoices@acme-studio.com`.
>
> 1. **Instant:** the Email Worker runs. The **first** thing it does is `message.forward(you@gmail.com)`, so delivery doesn't depend on anything else.
> 2. It saves the raw `.eml` to R2 and the parsed metadata to D1, matches the rule `Acme / Invoices` using the exact recipient address, and queues a job with a ~30s delay.
> 3. **Queue job:** runs the rule's pipeline on the stored copy: `clean` → Claude `extract:invoice` → `{vendor, amount, due}`.
> 4. **Gmail write-back:** finds the Gmail copy with `rfc822msgid:<Message-ID>`, applies the label `Acme/Invoices`, archives it, and inserts a digest into your inbox: `[Invoices] Acme — $1,240 due Oct 30`.
> 5. You open `inbox.<yourdomain>` (behind Cloudflare Access). The message is under **Acme → Invoices**, with the extracted fields, the original rendered safely and the attachments.

---

## 2. Architecture

```
 sender ─► Cloudflare Email Routing (per domain: catch-all → Worker)
                │
 ┌──────────────▼───────────── Worker "inbox" ──────────────────────────────┐
 │ email()      1. forward → you@gmail.com        (must succeed, first)      │
 │              2. raw → R2, parse → D1           (try/catch, never throws)  │
 │              3. Queue.send({id}, delay 30s)                               │
 │                                                                           │
 │ queue()      pipeline (clean / Claude extract / summarize) → D1           │
 │              Gmail write-back: find by Message-ID → label/archive/insert  │
 │                                                                           │
 │ fetch()      /oauth/*  connect Gmail (refresh token, AES-GCM encrypted)   │
 │              /api/*    JSON for the UI                                    │
 │              /*        read UI (static assets)                            │
 │                                                                           │
 │ scheduled()  daily: retry stuck write-backs, prune, health check          │
 │                                                                           │
 │ D1: messages, rules, transforms, gmail_actions, accounts                  │
 │ R2: raw/{id}.eml, att/{id}/{n}                                            │
 └───────────────────────────────────────────────────────────────────────────┘
            Cloudflare Access in front of inbox.<domain> (only you)
                                   │ Gmail API (gmail.modify)
                                   ▼
                                 Gmail
```

### Delivery safety (the critical rule)
The Worker is now in the delivery path, so `email()` follows a strict order:

```ts
async email(message, env, ctx) {
  await message.forward(env.FORWARD_TO);         // 1. deliver first
  try {
    const id = await ingest(message, env);       // 2. store + parse
    await env.QUEUE.send({ id }, { delaySeconds: 30 });
  } catch (err) {
    console.error("ingest failed", err);         // never rethrow: mail is already in Gmail
  }
}
```

- An exception **before** `forward()` can cause the message to be rejected. Nothing runs before it.
- If ingest fails, you still got the email. The daily cron can't recover the content, but the **backfill tool** (§6) can re-import any missing message from Gmail by date range.
- **Rollout:** switch one domain at a time from "Send to email" to "Send to Worker". Rollback is one click in the dashboard.

---

## 3. One-time setup

### Cloudflare
1. `wrangler d1 create inbox`, `wrangler r2 bucket create inbox-mail`, `wrangler queues create inbox-process`, `wrangler queues create inbox-dlq`.
2. Deploy the Worker. Point the custom domain `inbox.<yourdomain>` at it.
3. **Zero Trust → Access:** add a self-hosted app on `inbox.<yourdomain>` that allows only your email address (Google login or one-time PIN). Leave `/oauth/callback` inside Access as well (you're logged in when you click Connect).
4. For each business domain: **Email Routing → Routing rules → Catch-all → Send to Worker → `inbox`**. Your Gmail address is already a verified destination, since you forward to it today.

### Google (only for write-back and backfill)
1. Create a Cloud project and enable the **Gmail API**. You don't need Pub/Sub.
2. OAuth consent screen: External, scope `gmail.modify`. Set the publishing status to **"In production"**. In "Testing", refresh tokens expire after 7 days. You'll click through an "unverified app" warning once.
3. OAuth client (Web) with redirect URI `https://inbox.<yourdomain>/oauth/callback`.

### Secrets
`FORWARD_TO`, `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `TOKEN_ENC_KEY`, `ANTHROPIC_API_KEY`.

---

## 4. Routing rules (replacing Gmail filters)

The Worker knows the **real envelope recipient** (`message.to`), so matching is exact, including BCC and plus-addresses.

```ts
type Rule = {
  id: string; name: string; priority: number; enabled: boolean;
  match: {
    domain?: string;     // "acme-studio.com"
    local?: string;      // "invoices" | "*"   ("invoices+x" also matches "invoices")
    from?: string;       // "@stripe.com"
    subject?: string;    // regex
  };
  pipeline: string[];    // ["clean","extract:invoice","summarize"]
  gmail: {
    label?: string;      // "Acme/Invoices"
    archive?: boolean;
    markRead?: boolean;
    digest?: boolean;    // insert a transformed summary into your inbox
  };
};
```

- **Evaluation:** rules are checked in priority order and the first match wins. If nothing matches, the message goes to the `Unrouted` view, gets its domain as the default label, and has no pipeline.
- **Spam:** an optional rule-level `drop: true`. Global pre-checks (SPF/DKIM `fail` or a blocklisted sender) skip processing, but the message is **still forwarded**, so Gmail's spam filter gets the final say.
- **Migrating your filters:** **Rules → Import Gmail filters** reads `users.settings.filters.list` and turns the filters into draft rules for you to review. Run new rules in **label-only** mode until you delete the matching Gmail filter, so the two don't fight.
- **Test button:** dry-runs a rule against your last 100 stored messages and shows what would match.

---

## 5. Data model (D1)

```sql
CREATE TABLE messages (
  id            TEXT PRIMARY KEY,      -- ulid
  message_id    TEXT UNIQUE,           -- RFC822 Message-ID (dedupe key)
  received_at   INTEGER NOT NULL,
  source        TEXT NOT NULL,         -- 'edge' | 'backfill'
  env_to        TEXT NOT NULL,         -- envelope recipient
  to_domain     TEXT NOT NULL,
  to_local      TEXT NOT NULL,
  to_tag        TEXT,
  from_addr     TEXT, from_name TEXT,
  subject       TEXT, snippet TEXT, summary TEXT,
  in_reply_to   TEXT, thread_key TEXT,
  rule_id       TEXT,
  raw_key       TEXT NOT NULL, size INTEGER,
  status        TEXT NOT NULL,         -- stored | processed | failed
  gmail_id      TEXT,                  -- filled after lookup
  gmail_status  TEXT,                  -- pending | done | not_found | failed
  read_at       INTEGER, archived INTEGER DEFAULT 0
);
CREATE TABLE attachments (id TEXT PRIMARY KEY, message_id TEXT, filename TEXT, mime TEXT, size INTEGER, r2_key TEXT);
CREATE TABLE transforms  (message_id TEXT, kind TEXT, output_json TEXT, model TEXT, created_at INTEGER, PRIMARY KEY (message_id, kind));
CREATE TABLE gmail_actions (message_id TEXT, action TEXT, status TEXT, attempts INTEGER, last_error TEXT, at INTEGER, PRIMARY KEY (message_id, action));
CREATE TABLE rules (id TEXT PRIMARY KEY, json TEXT NOT NULL, priority INTEGER, enabled INTEGER);
CREATE TABLE accounts (id TEXT PRIMARY KEY, email TEXT, refresh_token_enc TEXT, connected_at INTEGER);
CREATE VIRTUAL TABLE messages_fts USING fts5(subject, from_addr, body_text, content='');
```

`thread_key` is the root Message-ID found by following `In-Reply-To`/`References`. If none is found, it falls back to the normalized subject plus the domain.

---

## 6. Components

### `email()`: ingest (small and boring)
- Forward → read `message.raw` → `R2.put(raw/{id}.eml)` → `postal-mime` parse → attachments to R2 → `INSERT messages` → enqueue.
- Use `INSERT … ON CONFLICT(message_id) DO NOTHING` so retries and backfill overlap are idempotent.
- Keep CPU low: no Claude calls and no Gmail calls here. Plan on Workers Paid ($5/mo) for headroom on large attachments.

### `queue()`: processing
For each job, every step is idempotent and keyed by `(message_id, step)`:
1. Load the rule and run the pipeline:
   - `clean`: HTML to text, then strip quoted history, signatures and tracking pixels.
   - `extract:<schema>`: Claude structured output against a per-schema JSON Schema.
   - `summarize`: a one-line summary for the list view and digests.
2. **Gmail lookup:** `messages.list?q=rfc822msgid:<id>&includeSpamTrash=true`.
   - Not found yet → `retry({ delaySeconds: 60 })`, with backoff up to ~15 min, then `gmail_status = not_found`. Usually this means Gmail hasn't received it yet or bounced it.
3. **Gmail actions:** ensure the label exists (cached), then `messages.modify` (add label, remove `INBOX`/`UNREAD` per the rule).
4. **Digest:** build MIME with `mimetext`, then `messages.insert` with `labelIds: ["INBOX","UNREAD",<label>]`. It isn't sent over SMTP; it just appears in your inbox. The From header is `Inbox <inbox@yourdomain>`, and the body links to the app.

After N failures the job goes to the DLQ, the message is marked `failed`, and it shows in the **Failed** view with a Retry button.

### `fetch()`: API + UI
- `GET /oauth/start`, `GET /oauth/callback`: connect Gmail and store the encrypted refresh token.
- `GET /api/messages?domain=&rule=&q=&cursor=`
- `GET /api/messages/:id` (metadata, clean text, transforms, attachments, Gmail status)
- `GET /api/messages/:id/raw`, `GET /api/attachments/:id`
- `POST /api/messages/:id/read|archive|reprocess`
- `GET/PUT /api/rules`, `POST /api/rules/:id/test`, `POST /api/rules/import-gmail`
- `POST /api/backfill { domains, since }`
- Defense in depth on top of Access: verify the `Cf-Access-Jwt-Assertion` header in the Worker.

### Read UI (Vite + React, built into Worker assets)
- **Sidebar:** grouped by domain, then rule, with unread counts. Plus All, Unrouted and Failed.
- **List:** sender, subject, Claude summary, time, attachment icon, Gmail-sync dot.
- **Message:** an extracted-fields card at the top, then the original HTML in a sandboxed iframe (no scripts, remote images off by default behind a toggle), then attachments, a collapsible headers panel and an "Open in Gmail" link (`#all/<gmail_id>`).
- **Rules:** list, edit, reorder, test, import from Gmail.
- **Settings:** Gmail connection status, connected domains, backfill (pick domains and a date range, with a progress bar), and a health panel (last message received, queue depth, failures in 24h).

### Backfill (one-time, and repair)
`messages.list q="to:(@a.com OR @b.com) after:YYYY/MM/DD"` → for each message: `messages.get?format=raw` → the same `ingest()` path with `source='backfill'`. Throttle to ~10 messages/s. Dedupe on Message-ID, so it's safe to re-run at any time to fill gaps.

### `scheduled()`: daily
Re-enqueue messages stuck in `gmail_status = pending` for more than 1h, prune R2 per the retention setting, and send a health digest if there were failures.

---

## 7. Repo layout

```
inbox/
  wrangler.jsonc                 # D1, R2, Queue + DLQ, cron, assets, routes
  package.json                   # hono, postal-mime, mimetext, @anthropic-ai/sdk, ulid
  src/
    index.ts                     # export default { email, queue, fetch, scheduled }
    email.ts                     # forward-first ingest
    ingest.ts                    # shared by edge + backfill
    queue.ts                     # pipeline runner + Gmail write-back
    rules.ts                     # matcher, Gmail filter import
    pipeline/{clean,extract,summarize}.ts
    http/{api,oauth}.ts          # Hono routes
    lib/{gmail,crypto,mime,access,ids}.ts
    schemas/invoice.json
  migrations/0001_init.sql
  web/                           # Vite + React UI
  test/
    fixtures/*.eml
    email.test.ts, rules.test.ts, queue.test.ts   # vitest-pool-workers, Gmail mocked
```

**Local dev:** `wrangler dev`, then `curl -X POST 'http://localhost:8787/cdn-cgi/handler/email?from=a@x.com&to=invoices@acme.com' --data-binary @test/fixtures/invoice.eml` runs `email()` without touching real DNS.

---

## 8. Milestones

| # | Deliverable | Done when |
|---|---|---|
| **M0** | Worker + D1/R2/Queue + Access on `inbox.<domain>` + forward-only `email()` | One test domain routes to the Worker and mail still arrives in Gmail |
| **M1** | Ingest: R2 + parse + D1 + attachments | Every new email on the test domain is in D1 with its raw copy in R2 |
| **M2** | Read UI: sidebar, list, message, attachments, search | You can browse and read the test domain's mail in the app |
| **M3** | Rules engine + Unrouted/Failed views + rule tester | Mail is grouped by your rules in the app |
| **M4** | Gmail OAuth + lookup + label/archive + filter import | Labels appear in Gmail; your filters are re-created as rules |
| **M5** | Claude pipeline (clean/extract/summarize) + digests via `insert` | Invoice fields appear in the app; a digest lands in your inbox |
| **M6** | Backfill + roll out to all domains + health panel + DLQ retry | Every domain goes through the Worker; history imported; failures visible |

Each milestone can ship on its own. M0–M3 need no Google setup at all.

---

## 9. Risks & mitigations

| Risk | Mitigation |
|---|---|
| A bug in the Worker blocks mail delivery | Forward is the first line; everything after is in try/catch; roll out per domain; one-click rollback in Email Routing |
| Worker CPU/memory limit on large mail | Workers Paid; no heavy work in `email()`; 25 MiB is Email Routing's hard cap |
| Gmail copy not found (delay, spam, bounce) | Delayed queue + backoff, search includes spam/trash, then `not_found` shown in the UI |
| Gmail refresh token stops working | "In production" OAuth status; "Reconnect Gmail" banner on `invalid_grant`; write-backs pause and resume |
| App rules conflict with Gmail filters | Label-only mode until each filter is imported and deleted |
| Gmail forward rejected (DMARC) | Cloudflare rewrites the sender for forwarding (SRS); the R2 copy is kept regardless |
| Malicious HTML / tracking pixels | Sandboxed iframe, no scripts, remote images blocked by default |
| Sensitive data exposure | Single user, Access + JWT check, refresh token encrypted, data stays in your Cloudflare account |
| Ingest failure loses the app's copy | Backfill-by-date-range repair tool; the email itself is always in Gmail |
| Mail sent directly to your @gmail address | Out of scope by design; backfill can import it ad hoc if needed |

---

## 10. Later
- **Reply as the business address:** Gmail "Send mail as" + an outbound provider (Cloudflare Email Service / Resend) with DKIM per domain.
- **Per-route webhooks** to external tools (accounting, CRM).
- **Retention policy** per rule (e.g. delete newsletters after 30 days).
- **Multiple destination inboxes** (e.g. forward `support@` to a teammate).

---

## Open decisions
1. Which domain hosts the app (`inbox.<?>`), and which domain do we use as the **M0 test domain**?
2. Backfill window: 30, 90 or 365 days?
3. Default for routed mail in Gmail: **label + archive**, or **label only**?
