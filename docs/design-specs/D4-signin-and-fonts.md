# D4 — server sign-in screen (D-012) and Open Runde font swap (D-011)

Written spec; source of truth (D-017). Tokens: `tokens.md` (v1.2). Units are pt. 390 pt reference width. "TA" = `accentText`, "AF" = `accentFill`.
**Supersedes** `D2-gap-specs.md` §1 (Sign in with Apple) and the OB01 footer line. Everything in D2 §1 that mentions Apple, Apple ID, `ASAuthorizationController` or `POST /v1/auth/apple` is void. (D2 is not edited here; this file wins.)

---

## Part 1 — Sign in screen (new, "OB01b")

### 1.1 Place in the flow

```
OB01 Welcome ── Get started ───────────┐
             └─ I already set this up ─┤
                                       ▼
                           OB01b Sign in (this screen)
                          ┌────────────┴─────────────┐
              mode = new  │                          │ mode = existing
                          ▼                          ▼
                       OB02 (Step 1)     Mail root, or resume at the step the
                                         server reports (onboarding_step)
```

- OB01 no longer signs in. Both buttons only navigate (push, standard slide). No spinner, no Banner, no disabled state on OB01. Remove the OB01 Banner code path.
- OB01 footer line (13.5 pt secondary, centred, `padding-top 8`) becomes: **"Next you'll enter your server address and password."** (Replaces "Sign in with Apple keeps this Inbox private to you.")
- The two buttons set the screen's **mode**: `new` (Get started) and `existing` (I already set this up). The screen is identical except title, caption and destination (table 1.4). A third entry, **signed out** (session lost, 401), opens the same screen in `existing` mode with the server address pre-filled, the caption of 1.4, and the password empty.
- Sign-in is once per device. On success the server returns a revocable device token; store it in the Keychain and the normalised server URL in app storage. **The password is never stored.**
- The screen has no "Step n of 12" label (it comes before step 1). Back (top left) returns to OB01. Back is always enabled; tapping it during loading cancels the request.

### 1.2 Layout (iPhone, 390 wide, light; all values are the same in dark with dark tokens)

Same scaffold as the other onboarding steps (`StepScaffold` metrics), scroll content with a pinned footer.

| Element | Spec |
|---|---|
| Ground | `ground` |
| Nav row | Back chevron (19 pt semibold) + "Back", TA, 17 pt, min height 44, left padding 16 (as other steps). No title in the bar. |
| Title | Heading style **h2**: Open Runde Bold 27/31, tracking −0.5, `text`, `padding-top 8`, horizontal 20, `header` trait. |
| Caption | Body 15/1.45 (lineSpacing 3), `secondary`, `padding-top 12`, horizontal 20. |
| Field group | `padding-top 24`. One inset card: `card` fill, radius 12, horizontal margin 16 (cards sit on the 16 gutter, text on 20), two rows, hairline divider between them (`hairline`, 1 pt, inset left 16). |
| Field row | min height **64**, padding 16 horizontal, 10 top/10 bottom. Stack: label (13.5, Medium, `secondary`, 1 line) then 4 pt gap then input (17, Regular, `text`). Input placeholder `textTertiary`. |
| Password row | Same row; input has a trailing **show/hide** button (44 × 44 hit area, 20 pt SF Symbol `eye` / `eye.slash`, colour `secondary`, 4 pt trailing padding inside the card). |
| Focus | The focused row gets a 2 pt `accent` ring, inset 2 pt from the row edge, radius 10 (outer rows: the ring follows the card radius at the card corners). Label of the focused row turns TA. |
| Helper under group | `padding-top 8`, horizontal 20, 13.5 pt `secondary`. |
| Inline error (when shown) | Replaces nothing; sits between group and helper: `padding-top 8`, horizontal 20, 14 pt exclamation-in-circle icon + text 13.5 **TA** (Medium). The errored row also gets a 2 pt `accent` ring (even when not focused). |
| Banner (lock-out, unreachable) | Standard `Banner`: `card` fill, radius 12, margin 8 16 6, padding 12 14, 15 pt text, 18 pt icon with status colour on the **icon** (warning `#B45309` / dark `#E0A03A`). Sits directly under the field group, `padding-top 12`. |
| Footer (pinned) | Horizontal 16, bottom 16 (above keyboard when up), `ground` background, no top rule. Contains the primary button. |
| Primary button | **Sign in**. Height 52, radius 14, full width, fill **AF** (`#C8281F` / dark `#FF5B45`), label 17 pt Medium `onAccent` (`#F1E9D2` / dark `#1A0F16`). |

Spacing summary top to bottom: nav 44 · 8 · title · 12 · caption · 24 · group (2 × 64 = 128) · 8 · helper · (scroll) · footer button 52 · 16.

### 1.3 Fields

| | Server address | Password |
|---|---|---|
| Label | **Server address** | **Password** |
| Placeholder | `inbox.example.com` | **Your server password** |
| Helper (shown under the group, one line each, 13.5 secondary) | **"The address you gave your Inbox server."** | **"Set on the server, not in this app. Forgot it? Change OWNER_PASSWORD there and restart the server."** |
| Keyboard | `.URL` (URL keyboard with `.` `/` `.com`) | default; secure |
| Autocapitalisation / autocorrect / spell-check | off / off / off | off / off / off |
| `textContentType` | `.URL` | `.password` (lets iCloud Keychain / password managers offer to save and fill) |
| Return key | **Next** (`.next`): moves focus to Password | **Go** (`.go`): submits if the button is enabled; otherwise does nothing |
| Secure | no | yes by default; the eye button toggles. Show/hide resets to hidden whenever the screen disappears or the app goes to the background. Toggling keeps focus and the caret position. |
| Paste | allowed | allowed |

Helper text is one combined block under the group: line 1 for the address, line 2 for the password (each 13.5 pt, separated by 4 pt), so it reads as one caption. Replace the password helper by the inline error when one is showing for that row; the address helper is replaced the same way.

**Server address handling (normalise on submit, show the result back in the field before the request):**
- Trim whitespace. If there is no scheme, add `https://`. Lower-case the host. Drop any path, query and trailing `/`. Keep an explicit port.
- `http://` is accepted only for `localhost`, `*.local`, `*.localhost` and private IPv4 ranges (10/8, 172.16/12, 192.168/16) so the dev server and LAN installs work. Any other `http://` host is an invalid URL (error below).
- Invalid means: empty host, spaces inside, no dot and not `localhost`/`*.local`, unsupported scheme (`ftp://` etc.), or non-local `http://`.
- Button enabled when both fields are non-empty (after trim for address; password is not trimmed). No validation while typing; validate on submit only. Clearing an error: as soon as the user edits the errored field.

### 1.4 Copy by mode

| | Title (h2) | Caption (15 secondary) | After success |
|---|---|---|---|
| `new` (Get started) | **Connect to your server** | **"Enter the address and password you chose when you set up your Inbox server. You only do this once on this phone."** | OB02 |
| `existing` (I already set this up) | **Welcome back** | **"Enter your server address and password to pick up where you left off."** | Mail root; if the server says onboarding is unfinished, resume at that step (OB02–OB12). |
| signed out (401 later, from the Mail root banner) | **Sign in again** | **"You were signed out of this server. Mail already on this phone stays readable."** | Back to where the user was (dismiss to Mail root). Address pre-filled and still editable; password empty and focused. |

"I already set this up" no longer differs by authentication path: it is the same server login, with a different destination. A new device of an existing install uses `existing`; the server decides what is left to do.

The Mail-root session banner (D2 §1.6) keeps the lead **"You've been signed out"** and action **Sign in**, which now opens the sign-in screen in the signed-out mode above (not an Apple sheet).

### 1.5 States and errors

Only one error surface is visible at a time. A new attempt clears the previous one. Keep what the user typed except where noted.

| State | Trigger | Surface and copy | Fields / button |
|---|---|---|---|
| **Empty** | Initial | No error. Button dimmed (opacity .4, not hit-testable). | Focus: Server address (`new`) or Password (`existing`/signed out with address already filled). |
| **Ready** | Both fields non-empty | Button full opacity. | |
| **Loading** | Submit | Button keeps size, label hidden, 17 pt spinner `onAccent`. Keyboard dismisses. No Banner. | Both fields disabled at opacity .6 and not editable; show/hide still works. Request timeout **15 s** then unreachable. |
| **Invalid URL** | Client-side validation fails on submit (no request sent) | Inline error under the group: **"That doesn't look like a server address. Try something like inbox.example.com."** For non-local `http://`: **"Use https:// for a server on the internet."** Address row gets the ring. | Focus returns to Server address with the text selected. Password kept. |
| **Wrong password** | 401 `invalid_credentials` | Inline error under the group: **"That password isn't right."** Password row ring. If the server returns attempts left and it is ≤ 2, append on the same line break: **"2 tries left before sign-in pauses."** (backend decision, see 1.9) | Password cleared, focus in Password, secure mode restored to hidden. Address kept. |
| **Too many attempts / locked out** | 429 with `Retry-After` | Banner (warning clock-pause icon), lead **"Too many tries."** + **"Sign-in is paused to protect your server. Try again in 4:32."** Live countdown `m:ss` under 10 min; 10 min or more: **"…Try again in about 14 minutes."** (round up, recomputed each minute); 1 h or more: **"…in about 1 hour."** Countdown uses `monospacedDigit`. | Button disabled and reads **"Try again in 4:32"** (same countdown; at ≥10 min **"Try again later"**). Fields stay editable. At 0 the Banner disappears, the button returns to **Sign in** and enabled. No auto-retry. Lock-out survives leaving and returning to the screen in the same launch (store the retry-at time in memory). |
| **Server unreachable** | Offline, DNS failure, timeout, connection refused, TLS failure, or 5xx | Banner (warning triangle icon), lead **"Can't reach your server."** + **"Check the address and your connection, then try again."** Action TA **Try again** (re-submits). TLS failure message: **"Couldn't make a secure connection to inbox.example.com."** (use the host). 5xx message: **"Your server answered with an error. Try again in a moment."** | Fields editable, button enabled **Sign in** (so the user can fix the address). |
| **Not an Inbox server** | 2xx/404 but response is not the Inbox API (no `/v1` JSON) | Inline error under the group, address ring: **"That address doesn't look like an Inbox server."** | Focus Server address. |
| **Success** | 200 with token | Keep the spinner until the next screen has pushed (no flash of the form). Haptic: none. | |

VoiceOver on errors: post `UIAccessibility.post(.announcement, …)` with the error text, then move focus to the error element (inline error or Banner). Banner and inline error are one accessibility element with the full sentence.

### 1.6 Colours (tokens.md)

| Element | Light | Dark |
|---|---|---|
| Title / input text | `text` `#461634` | `#F3EBD8` |
| Caption, labels, helper, eye icon | `secondary` `#7A5268` | `#C2AFB8` |
| Placeholder | `textTertiary` `#7E576C` (5.38 on card) | `#907D87` (4.55) |
| Field card | `card` `#F7F1E2` | `#24151F` |
| Divider | `hairline` `#DCCFBC` | `#36222F` |
| Focus / error ring | `accent` `#F83D34` (non-text) | `#FF5B45` |
| Back, Try again, focused label, **inline error text** | TA `#BD251D` (5.41 on card, 5.03 on ground) | `#FF5B45` |
| Button fill / label | AF `#C8281F` / `onAccent` `#F1E9D2` (4.58) | `#FF5B45` / `#1A0F16` (6.08) |
| Banner icon | `warning` `#B45309` | `#E0A03A` |
| Banner fill | `card` | `card` |

Error text is TA, **not** `destructive`: `#D92D20` on cream is only 3.99:1 (see tokens.md D-013).

### 1.7 Dynamic Type and Accessibility sizes

- All text uses the scaled Open Runde styles from Part 2, relative to the matching text style (title: `.title2`, caption/helper: `.subheadline`/`.footnote`, input and button: `.body`).
- Up to XXXL: layout as in 1.2; field rows grow with their text (min 64).
- **AX1 and larger:** the primary button moves from the pinned footer into the scroll content, directly after the helper block (`padding-top 24`, bottom 24), so a tall button plus keyboard cannot cover the fields. Scroll view uses `scrollDismissesKeyboard(.interactively)`. The focused field scrolls into view.
- AX3: title 27 → about 53 pt after scaling, wraps to as many lines as needed (no truncation, `lineLimit(nil)`). Caption, helper and inline error wrap fully. Banner wraps fully; its action moves to its own line below the text. Field labels stay 1 line with tail truncation only above AX3; at AX3 allow 2 lines. **Input text is one line and scrolls horizontally** (it is an editable field); the placeholder may truncate with tail (`inbox.example.com` is the longest and fits; the password placeholder is allowed to shorten to **"Password"** at AX3). Eye button stays 44 × 44.
- Button label: 1 line normally; at AX3 up to 2 lines; height grows (min 52), radius stays 14. Locked-out label **"Try again in 4:32"** is the long one: allow 2 lines.
- Reduce Motion: no spinner change (spinners are allowed); the focus ring appears without animation. Otherwise nothing animates beyond the standard push.
- Light/dark and Increase Contrast: use the same tokens; with Increase Contrast the ring is 3 pt.

### 1.8 VoiceOver

| Element | Label | Value / hint | Traits |
|---|---|---|---|
| Back | "Back" | | button |
| Title | the title text | | header |
| Caption | text | | static text |
| Server address | **"Server address"** | value = text or "empty"; hint **"The address you gave your Inbox server, for example inbox.example.com."** | text field |
| Password | **"Password"** | value announced as secure by the system; hint **"Set on the server, not in this app."** | secure text field |
| Show/hide | **"Show password"** / **"Hide password"** | | button |
| Inline error | the error sentence, prefixed **"Error:"** | also set as the accessibility value of the errored field | static text |
| Banner | lead + message, e.g. "Too many tries. Sign-in is paused to protect your server. Try again in 4 minutes 32 seconds." | | static text; action as separate button **"Try again"** |
| Sign in | **"Sign in"** | | button; dimmed when disabled |
| Loading | **"Signing in"** | | button, not enabled, `updatesFrequently` |
| Locked button | **"Try again in 4 minutes 32 seconds"** | | button, not enabled; countdown is spoken in words and announced only at start and when it reaches 0 (not every second) |
| Decorative icons (banner icon, exclamation, spinner) | hidden | | |

Focus order: Back, title, caption, Server address, Password, Show/hide, helper/error, Banner (if any), Sign in. On entering the screen VoiceOver focuses the title.

### 1.9 Backend contract this screen assumes (Swift/backend to confirm)

- `POST /v1/auth/login` `{ password, device_name }` to the entered base URL → `200 { token, device_id, onboarding_step }`.
- `401 invalid_credentials`, optional `attempts_remaining`; `429` with `Retry-After` seconds; anything else is "unreachable".
- Constant-time compare and lockout are server-side (D-012); the app only displays them.

---

## Part 2 — Open Runde (D-011)

### 2.1 Fonts

| Role | Was | Now |
|---|---|---|
| Headings | PolySans Neutral Bold | **Open Runde Bold** (and Semibold for small headings, see table) |
| Body regular | TWK Lausanne 300 | **Open Runde Regular** (400; the lightest weight Runde ships) |
| Body medium | TWK Lausanne 500 | **Open Runde Medium** (500) |
| Mono | GT America Mono | **System monospaced** (`Font.system(size:, design: .monospaced)`), Regular. Not licensed, not bundled. |

Files: `OpenRunde-Regular`, `-Medium`, `-Semibold`, `-Bold` as OTF/TTF (the `/fonts` woff2 files cannot be loaded by iOS). Confirm the PostScript names from the files (Font Book or `UIFont.fontNames(forFamilyName:)`) and use those exact names in `InboxFont`. Open licence (SIL OFL): ship the licence text in Settings → About/Acknowledgements. Keep the existing fallback: if a face is missing, use the system font at the same weight.

### 2.2 Why sizes change

Open Runde is a rounded Inter-style face: larger x-height and ~4–6% wider advance widths than Lausanne at the same size, and heavier at Regular than Lausanne 300. Result: same point size looks bigger and wraps sooner. Rules:
1. Body and row text drop by 0.5 pt (or 1 pt at ≥17) so line lengths stay close to the boards.
2. Large Bold headings drop 1–2 pt and tracking tightens (Runde's rounded caps and wide counters open up at display size).
3. Line height is expressed as a multiple, so it is reduced by ~0.03–0.05 where the x-height is bigger.
4. Never drop below 13 pt for text. Prefer truncation rules in the table to shrinking further.
5. Tabular figures: use `.monospacedDigit()` for countdowns, times in a fixed column, counts and badges (verify the face has `tnum`; if not, fall back to the system font for digits in those spots).

### 2.3 Type style map (every style in tokens.md and the onboarding code)

| Style | Was (family, pt/line, tracking) | Now (Open Runde) | Notes |
|---|---|---|---|
| Onboarding h1 (OB01) | PolySans 700, 34, −0.7 | **Bold 32/36, −0.8** | `.largeTitle`. Wraps to 3–4 lines at AX3: OK. |
| Title (nav large) | PolySans 700, 32/35, −0.02em | **Bold 30/34, −0.025em (−0.75)** | |
| h2 (onboarding step titles, Gmail "Connect Gmail", sign-in) | PolySans 700, 28, −0.4 | **Bold 27/31, −0.5** | |
| Subject (message) | PolySans 700, 21/25 | **Semibold 20/25, −0.2** | Semibold keeps width in check. |
| Sheet title | PolySans 700, 21 | **Semibold 20/24** | |
| Row sender (read / unread) | Lausanne 300 / 500, 16 | **Regular 15.5 / Medium 15.5** | Unread stays Medium. Truncate tail, 1 line. |
| Row title / list row primary (BZ, settings, domains) | Lausanne 300, 16.5 | **Regular 16/ line 20** | |
| Row subtitle / domain / secondary line | Lausanne 300, 14 | **Regular 13.5** | |
| Subject line in list | Lausanne 300, 15 | **Regular 14.5** | |
| Preview | Lausanne 300, 15 secondary | **Regular 14.5** | 2 lines max, tail. |
| Time / date | Lausanne 300, 14 secondary | **Regular 13.5**, `monospacedDigit` | Fixed column: widen 4 pt (see risks). |
| Section header (caps) | Lausanne 500, 12.5, +0.04em | **Medium 12, +0.05em** (caps look wider; track slightly more but smaller) | Never below 12. |
| Button | Lausanne 500, 17 | **Medium 17** (unchanged size; short labels) | |
| Body / message text | Lausanne 300, 16 / 1.55 | **Regular 16 / 1.5** | |
| Caption / onboarding body | Lausanne 300, 15 / 1.45 | **Regular 14.5 / 1.45** | OB01 intro (17) → **16.5**. |
| Footnote / helper | Lausanne 300, 13.5 | **Regular 13 / 1.4** | Smallest text size. |
| Chip | Lausanne 500, 14 | **Medium 13.5** | |
| Nav title (centred) | Lausanne 500, 17 | **Semibold 16.5** | |
| Back / Cancel / links (TA) | Lausanne 500, 17 | **Medium 17** | |
| Banner text | Lausanne 300/500, 15 | **Regular/Medium 14.5** | Lead is Medium. |
| Field label (new, sign-in) | – | **Medium 13.5** | |
| Field input | Lausanne 300, 17 | **Regular 17** | |
| Badge / count | Lausanne 500, 12 | **Semibold 12**, `monospacedDigit` | |
| Mono (logs, codes, URLs) | GT America Mono 12 | **System mono Regular 12** | Wraps, never truncates logs. |
| Tab bar labels, system alerts, nav bar items, keyboard | system | unchanged (system font) | Open Runde is not applied to system chrome. |

All sizes are base sizes at Large; keep scaling with Dynamic Type through the same `relativeTo` styles as today.

### 2.4 Truncation and wrapping risks

1. **Mail row, sender + time** (ML): sender at Medium 15.5 plus a 13.5 date; "Yesterday", "Wednesday" can be 6% wider. Give the time column `minWidth` +4 pt, let the sender truncate (tail). At AX3 the date goes to its own line under the sender.
2. **AX3 anywhere with Bold headings:** "One quiet inbox for all your businesses." at 32 is about 10 lines at AX3; ensure ScrollView and no `lineLimit`. Nav large titles ("Businesses", "Needs attention") use `lineLimit(2)` and `minimumScaleFactor` none; centred inline titles truncate tail at AX3.
3. **Chips** ("Needs attention", "Unrouted"): width grows ~5%; scroll horizontally (already); do not shrink text.
4. **BZ02 edit rows**: name 16 + domain 13.5 next to a 48 × 28 switch and drag handle; domain truncates middle at XL+; name wraps to 2 lines at AX.
5. **Message header**: To/address lines, `billing@acme-studio.com` – keep `lineLimit(1)` + middle truncation (existing S-5 rule); Runde makes this trigger earlier (about AX1).
6. **Segmented control / switch rows / settings values** (right-aligned values like "Stored copies · 1 year"): value truncates first, label wraps; check "Delete everything Inbox stored".
7. **Buttons with long labels**: "Try again in 4:32" (sign-in lock), "Delete everything", "Continue with Google": allow 2 lines at AX, never shrink.
8. **Section headers in caps** at 12: may wrap at AX3 ("WHERE YOUR MAIL LIVES"); allow 2 lines.
9. **Badges/counts** (three digits): use `monospacedDigit`, min width 20, padding 6.
10. **Mono logs (NA05)**: mono is unchanged in width class; wrap, do not truncate.
11. **Heading width in Business page title with long domains** (`acme-studio.com`): single line, middle truncation, AX: wrap.
12. **Fallback mixing**: if only some faces load, rows will look uneven; the font check test in DESIGN_QA gate must fail the build when any of the four faces is missing.

---

## Part 3 — Implementation list for Swift

See `docs/handoff/design-to-swift.md` (2026-10-31 entry) for the ordered list.
