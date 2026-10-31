# D2 — design gap specs (design agent)

Written specs (the canvas boards OB01, ST04, NA05, AF01–AF04, OB10 keep the old copy; **this file is the source of truth for these screens until the canvas is updated**). Tokens: `tokens.md`.

## 1. Sign in with Apple (before OB01 continues)

> **VOID (D-012, 2026-10-31).** Sign in with Apple is replaced by server address + password. See `D4-signin-and-fonts.md`. Kept below as history only.

No custom sign-in screen; OB01 stays as drawn. Flow:

1. OB01 shows **Get started** (primary) and **I already set this up** (plain). Both open the **system Sign in with Apple sheet** (`ASAuthorizationController`, scopes none; we only need the stable `sub`).
2. While the system sheet is up and while `POST /v1/auth/apple` runs, the tapped button shows an inline spinner (button keeps its size, label hidden, 17 pt spinner `onAccent`), the other button is disabled (opacity .4).
3. Success: *Get started* → OB02. *I already set this up* → straight to Mail root (server has `onboarding_step = done`); if the server says onboarding is unfinished, resume at that step (OB02–OB12).
4. Errors (shown as the standard `Banner`, warning dot, above the buttons, 15 pt; action = TA `Try again`):
   - User cancelled the sheet: no banner.
   - Not allow-listed (403): title **"This Apple ID isn't set up here"**, message **"Ask whoever runs this Inbox server to add you, or sign in with a different Apple ID."**, no action.
   - Network/server (5xx, offline): title **"Couldn't sign in"**, message **"Check your connection and try again."**, action **Try again**.
5. Footer under the buttons, 13.5 pt secondary, centred (new line on OB01): **"Sign in with Apple keeps this Inbox private to you."**
6. Session lost later (401, token revoked): quiet Banner on the Mail root, **"You've been signed out"** + action **Sign in**, which re-runs step 1; mail already on the phone stays readable.

## 2. Privacy copy (ST04)

"Where your mail lives" group, rows (title 16.5 / subtitle 14 secondary):

| Row | Title | Subtitle |
|---|---|---|
| 1 | Your Inbox server | Stored copies and the search index |
| 2 | Your Cloudflare account | Receives your mail and passes it on |
| 3 | Your Gmail | Unchanged, except labels and archive |
| 4 | Alfather | Reads from your Inbox server; never sent to a cloud AI |

Rest unchanged: **Keep** → *Stored copies* · *1 year*; **Delete everything Inbox stored** (destructive); footnote **"Deleting stored copies never touches Gmail."** Remove all mention of "R2" and "D1" from user-facing copy (also RU/BZ04 "Remove": keep "stored copies", no storage product names).

## 3. NA05 failed detail (example copy)

A 31 MB message cannot occur (Email Routing caps at 25 MiB), so the example becomes a store failure. Layout unchanged.

- Title: **"Couldn't store."**
- Body: **"The server couldn't save this message. It was still delivered to Gmail."**
- Message block: From *Lumen Hosting* · To `billing@acme-studio.com` · Received *Sep 12, 4:10 PM*
- What happened (mono 12, as drawn):
  ```
  09:41:07 received 4.2 MB
  09:41:07 forwarded to Gmail ✓
  09:41:08 store raw → failed (storage unavailable)
  09:41:08 marked failed
  ```
- Actions: Open in Gmail · **Retry** (primary) · Dismiss.
- Variant when parsing fails: title **"Couldn't read this message."**, body **"It arrived, but its format couldn't be understood. It was still delivered to Gmail."**, log line `09:41:08 parse → failed (malformed MIME)`; Retry hidden (retry cannot help), Open in Gmail + Dismiss only.

## 4. Alfather — connect-out pairing (D-006)

Model: phone discovers Alfather on the LAN and **pairs once**; afterwards Alfather connects out to the Inbox server. The phone only needs to be nearby during pairing.

- **AF01 Set up** — intro: **"Alfather is your local assistant. Inbox pairs the two once; after that Alfather connects to your Inbox server and reads new mail in the background."** Steps: **1 · Start Alfather on your Mac** · **2 · Keep this phone on the same Wi-Fi for now** · **3 · Tap Look for Alfather**. Footnote (13.5 secondary): **"After pairing, your phone doesn't need to be nearby."**
- **Pairing in progress** (new, same screen, button spinner): **"Pairing with Alfather…"**
- **Pairing failed** (new, Banner): **"Couldn't pair"** / **"Alfather didn't accept the pairing. Check it's running and try again."** / action **Try again**.
- **OB10 Alfather found** — body: **"Your local assistant is running on this network. Inbox will pair with it; it then reads new mail from your Inbox server in the background."** Row: *Alfather* · **"Found · alfather.local"** (was "Connected"). Footnote unchanged ("Nothing it reads shows in the app yet…"). **Continue** performs the pairing; on failure show the pairing-failed Banner on this screen.
- **OB11 Not found** — keep, add to the existing hint: **"You can pair later in Settings → Alfather."**
- **AF02 Connected** — subtitle: **"Connected to your Inbox server · last seen just now"** (was "alfather.local · last seen just now"). Rest as drawn.
- **AF03 Reconnecting** — **"Reconnecting… Alfather will pick up where it left off."**
- **AF04 Not reachable** — title unchanged; subtitle **"Last seen yesterday, 6:40 PM"** (= last MCP activity, threshold 5 min); note **"Mail is unaffected. Alfather reads it when it reconnects; nothing is lost."**; **Try again** re-checks status; **How to connect** opens a sheet: "Make sure Alfather is running on your Mac and signed in to your Inbox server. If it still doesn't connect, pair again from this phone on the same Wi-Fi." with a **Pair again** button.
- Terminology: "Connected" always means *Alfather is connected to the Inbox server*; never imply the phone connects to Alfather.

## 5. Accessibility / text accent

Implemented as tokens (`tokens.md`, rules G1–G3 in `DESIGN_QA.md`). Applies to every accent text in the above flows (Try again, Sign in, Pair again, Retry label on secondary, links).
