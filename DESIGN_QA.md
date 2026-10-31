# Gate 12: PASSED after round D4 (2026-10-31)
P0 0, P1 0. Both P1 (delete-sheet disabled button, sender HTML at Dynamic Type) and all 20 listed P2 items were re-viewed in the D4 snapshots and are closed; nothing reopened. Remaining P2 polish is accepted (list in `docs/design-audit/GATE12.md` §9). Hand-verification list: GATE12 §6.

# Gate 12 first result (before D4, 2026-10-31)
Audit after the font swap and the D1 to D3 rounds, all light snapshots viewed, about 40 dark, 36 AX3 light and 7 AX3 dark: **P0 0, P1 2, P2 24 (20 in the fix list, 4 left out as polish).** All Gate 11 items H-1..H-17 are closed. The two P1 are the delete-everything sheet's disabled button, which is unreadable (invisible in dark), and sender HTML in a message that does not scale with Dynamic Type. The ~30 D3 findings are all graded: 8 closed by written spec (new `docs/design-specs/D6-gate12-decisions.md`, 21 numbered decisions the owner can overrule), the rest are in the fix list or left out. The gate does not pass yet; one Swift round (ordered list with snapshot proofs in `docs/handoff/design-to-swift.md`) fixes it. Details, per-screen table and the hand-verification list: `docs/design-audit/GATE12.md`.

---

# Gate 11 summary (2026-10-31)
Full screen audit against boards, D2-D4 and tokens v1.2: **P0 0, P1 8, P2 9.** Sign-in (OB01b) matches D4 in layout, copy, states and AX3. Main gaps: Open Runde size/weight table (D4 §2.3) is not applied, red text still uses `destructive`/`accent` in 10 places, AX3 large title breaks mid-word, locked sign-in button is unreadable, OB07/OB12 not built, and about 40 screens have no current snapshot. Details: `docs/design-audit/GATE11.md`; ordered fixes: `docs/handoff/design-to-swift.md`; proposals: `docs/design-specs/D5-proposals.md`.

---

# Design status (for the owner) — updated after Gate 7

> **HISTORICAL (superseded 2026-10-31).** The "Known gaps" and "Decisions needed" below are settled: Open Runde replaces the unlicensed fonts (D-011), password sign-in replaces Sign in with Apple (D-012), `#BD251D` is approved for red text (D-013), and Gate 12 passed (see `docs/design-audit/GATE12.md`). Current state is in the Gate 11 and Gate 12 summaries above.

**Phase 2 update (2026-10-12):** all Gate 8 P1s (BZ02 edit rows, AX3 message header, relative dates) are fixed and verified in Gate 9; no P0/P1 open. The `Spec updates` canvas page (SU01–SU08, light only) shows the new specs; original boards are untouched. Still not reviewed for lack of screenshots: Message More/Details/Original/attachment, rules-editor states RU02/06/07/09/10, onboarding OB03/04/06/07/11–13, swipe/select/refresh, dev-server mode beyond one shot. Open P2s: system drag handle (BZ02), Business-page AX3 subline separator, and the Gate 4/6 backlog.

**Where we are:** the iOS app now matches the design boards for every screen built so far (Businesses, Business page, Message, Search, Needs attention, Rules, Gmail, Onboarding, Businesses/Settings/Health/Alfather, Import, Privacy), in light and dark. No P0 or P1 defects are open. Checked by comparing simulator screenshots and Swift code to the boards; I did not run the build.

**Known gaps**
- **Fonts (D-005):** the app still uses the system font. PolySans, Lausanne and GT America Mono are not bundled until you confirm the licences allow app embedding. The files in `/fonts` are woff2, which iOS cannot load, so they also need converting to OTF/TTF.
- **Canvas boards not updated:** the new specs (Sign in with Apple step, privacy copy, failed-item example, Alfather pairing copy, text-accent colour, Gmail rate-limited and not-connected states, delete-everything sheet) live in `docs/design-specs/`. The live canvas boards still show the old copy and colours, so the written specs are authoritative.
- **P2 backlog: about 15 polish items** (Gate 4 and Gate 6 lists): date formats, Appearance selected-state circle, Health row order, onboarding card inset, delete-sheet placeholder text, a few spacing details. None block shipping.
- **Not yet reviewed:** screens not built yet, and the remaining Message sheets (More, Details, Original).

**Decisions needed from you**
1. Font licences: allow app embedding of PolySans, Lausanne and GT America Mono, or choose substitutes.
2. Approve the new text-accent colour `#BD251D` for text (the brand red `#F83D34` is 3:1 on cream and fails accessibility).
3. Confirm Sign in with Apple as the sign-in method (needs an Apple Developer account).
4. Decide whether I should update the canvas boards to match the specs, or leave the written specs as the source of truth.

---

# DESIGN_QA.md — fidelity audit D1 (design agent)

Scope: built screens T11–T15 (`ios/docs/*.png`, `ios/InboxKit/Sources/`) vs canvas boards (live canvas files, light variants; `…d` dark variants share geometry). Units: pt = board px (390 pt wide). Tokens come from `D-Foundations`; see `docs/design-specs/tokens.md` for the new/changed values referenced below (**TA** = text accent, **AF** = accent fill).
Severity: P0 wrong/broken · P1 visible mismatch · P2 polish. Status column: `open` until the D3 gate passes it.

Not auditable yet (no screenshot): Needs attention (NA01), swipe/select/refresh/skeleton/empty states (ML05–ML11), Attachment (MS05), More/Details/Original (MS06–08), Search scoped/no-results (SR03/04), light-mode Businesses/Message. Swift: please add light + dark screenshots for each when you re-submit.

## Global rules (apply to every row below)

- **G1 Text accent.** Accent `#F83D34` is 3.04:1 on ground. Any accent-coloured **text** (back label, Cancel, Clear, Fix, Undo, Load images, links, selected tab label) uses **TA**: light `#BD251D` (5.03 on ground, 4.55 on grouped), dark `#FF5B45` (unchanged, 6.08). Icons, dots, bars, switch fill keep `#F83D34`.
- **G2 Tertiary text.** `tertiary` (`#A68C97` = 2.54:1) is non-text only (chevrons, placeholder icon, disabled). Text that the boards draw in tertiary (timestamps, section headers, counts, "To:" line) uses `secondary` `#7A5268` / dark `#C2AFB8`. Placeholder text (search field): light `#7E576C` (4.5:1 on `field`), dark `#907D87`.
- **G3 Filled controls.** Light-mode filled control fill (primary button, selected chip) = **AF** `#C8281F` with on-accent `#F1E9D2` (4.58:1). Dark mode keeps `#FF5B45` fill with `#1A0F16` text.

## P0

| # | Board | Design shows | App does | Fix | Status |
|---|---|---|---|---|---|
| 1 | MS01, MS03, MS04 (T14 all three shots) | Message screen has **no tab bar**. Bottom is one action bar `.tbar`: height 84 (50 + 34 safe), top hairline 1pt `hairline`, bg `ground`, `padding 12 34 0`, 4 **icon-only** items (22 pt, stroke 1.6) in **accent**, spaced `space-between`: Archive (box), Read/Unread (hollow circle r 4.5), Open in Gmail (arrow out of box), More (three dots). | Tab bar (Mail/Rules/Settings) **and** a 3-item labelled toolbar (Archive / Mark unread / More, 13 pt text, `card` bg) are stacked; a dark rounded shape from the scroll content pokes above the toolbar (y≈730 pt); labels clip against the tab bar. | Hide the tab bar on Message (`.toolbar(.hidden, for: .tabBar)`). Replace toolbar with the `.tbar` spec above; icons as drawn on the board (archive box, circle, arrow-out-of-box, three dots; SVG paths in `docs/design-ref/MS01.dc.html` `.tbar`); `accessibilityLabel` on each ("Archive", "Mark unread", "Open in Gmail", "More"). Content must scroll under/stop above the bar (`safeAreaInset(.bottom)`), no overlap. | open |

## P1

| # | Board | Design shows | App does | Fix | Status |
|---|---|---|---|---|---|
| 2 | All | Fonts: titles/subjects **PolySans 700**, body **Lausanne 300/500**, mono only for Original/Details raw headers (**GT America Mono**). Title `lt` 32/35 `-0.02em`; subject 21/1.25 `-0.01em`; sheet title 21. | System font everywhere (fonts unbundled, D-005). Fallback headings use `.semibold`; subject is 26 pt, Businesses title is system Large Title 34. | (a) Decide licence (coordinator/owner); until then fallback must match weight: `inboxHeading` fallback `.bold`, sizes below. (b) When bundled: convert woff2 → OTF/TTF (iOS cannot load woff2), set real PostScript names (current names are guesses). | open |
| 3 | ML01 | Large title 32/35, left pad **20**, no nav-bar title chrome. | System large title 34, left 16. | Custom title view: `Font 32 bold`, `tracking(-0.64)`, leading padding 20 (all list/inset content margin = **16**, text inside rows = 20). | open |
| 4 | ML02, ML03, ML04, MS01–04, NA/RU… | Nav row 44 pt: **plain** accent chevron (24 pt, stroke 1.6) + **previous screen's title** (17 pt, TA), actions in accent, no capsule. | iOS 26 glass circle back button (no label), glass pill for actions, icons in `text` colour. | Hide glass: custom `ToolbarItem` with `.buttonStyle(.plain)` + `.sharedBackgroundVisibility(.hidden)`; back label = parent title (TA, 17). Padding `0 16 0 10`. | open |
| 5 | ML02/03 | Business page: title 32 + subline `acme-studio.com · 2 unread` in **body font 15, secondary**. | Subline in mono 13 `secondary`. | `inboxBody(15)`, `secondary`. | open |
| 6 | ML02/03 | Chips: h **32**, h-pad 13, radius 16, 14.5 pt, count 13.5; **unselected = transparent + 1 pt inset ring `#CDBDA5`/dark `#36222F`+**; selected = fill AF, text on-accent. Row padding `14 20 8`, gap 8. | h 36, 15 pt, selected fill = `text` colour (cream chip in dark), unselected filled `grouped`, row inset 16. | Per spec (G3 for selected fill; ring colour new token `hairlineStrong`: light `#CDBDA5`, dark `#4A3342`). | open |
| 7 | ML02 rows | Row `padding 11 20 11 12`, unread dot 9 pt at x=12, text column starts **x=32**, hairline from x=32, 1 pt. Sender 16 (400 read / **500 unread**), time 14 secondary(G2), subject 15 **weight 400 always**, preview 15 secondary 2 lines. | Text starts x=48; dot 10; sender 17; time tertiary-ish; **subject medium when unread**; hairline 0.5 from 48; v-pad 12. | `MailRow`: leading 12, dot 9, gap 10 (text x=32), trailing 20, v-pad 11, sender 16, time 14 `secondary`, subject 15 regular, hairline `1 pt` from 32. | open |
| 8 | ML01 | Root: inline **search field** (h 38, r 10, fill `field` `#E5D9BC`/dark `#2C1B26`, magnifier 18, "Search" placeholder), "Edit" action top-right (accent). "All businesses" inset row (tray icon 22 accent, 16.5, count right **16 secondary**), header `12 businesses` (13 pt sans, secondary, caps, tracking .03em, `padding 22 32 7`), inset rows min-height **56**: name 16.5 / domain 14 secondary / count 16 **secondary (not accent)** / chevron `#CDBDA5` 18 pt. | Search is a nav icon only; no Edit; "All businesses" row has no icon; headers mono 12; counts **accent 15 medium**; rows ≈48 pt; chevron `footnote.semibold` tertiary; group radius 14. | Per board. Accent counts only on the Needs-attention group (NA01 uses accent counts). Inset radius **12**, row min-height 56, section header per `.igh`. `Edit` → BZ02 (can ship disabled until T24). | open |
| 9 | Foundations, ML01, all | Section headers sans (Lausanne 500 12.5 caps, tracking .04em, tertiary→G2 secondary) — mono used **only** for raw headers/code. | Mono 12 for InsetGroup headers, search section headers + counts, email addresses in Message header, thread count. | Replace `inboxMono` outside Original/Details with `inboxBody(12.5, medium: true)` + `.tracking(0.5)`; email address lines 14 pt **body** `secondary`. | open |
| 10 | Tab bar (all) | `.tab` 84 pt: **icon 26 pt (stroke 1.6) over label 11 pt/500**, `padding-top 8`, gap 3; active: icon+label **accent (TA for label)**, 30×3 bar at top (`radius 0 0 3 3`, flush with the hairline, `top −1`). | Text-only tabs 14 pt, 20×3 capsule floating 6 pt above label, active label `text` colour. | Add icons (mail-tray, sliders `M4 7h16M7 12h10M10 17h4`, gear), 26 pt; label 11/500; bar 30×3 at `top:-1`, `UnevenRoundedRectangle(bottom 3)`; hairline 1 pt. Dark: bar `#FF5B45`. | open |
| 11 | MS01/MS03/MS04 header | Back label = business name (TA). Right actions: ▲ ▼ prev/next message (accent). Header: avatar **40 pt** circle `grouped`, initials 500 secondary (e.g. "NL"); sender 16/**500 text colour**; address 14 secondary (sans); `To:` 14 secondary; time `9:41 AM` 14 secondary; **1 pt hairline under header**; subject **21/25 PolySans 700, tracking −0.2**, margin-top 16, pad `8 20 14`. | Centered nav title; no prev/next; avatar 44ish with 1 letter; sender and time render at ~tertiary (header block looks 50% dimmed in T14-invoice/images shots); address mono 13; `To:` mono; subject **26**; no hairline. | Per spec. Check the dim: header must not inherit `.opacity`/`secondaryLabel`. Prev/next may be disabled when unavailable (still shown). | open |
| 12 | MS03 | Thread title **21** bold + count `4` 15 secondary (not mono); collapsed rows `pad 12 20`, sender 16/500, **day 14 secondary**, one-line snippet 15 secondary, 1 pt hairline **full width**; open message: sender 500, `To:` 14 secondary, body 16/1.55. No avatar/address in the open card. | Title 26, count mono 13; open message repeats avatar + mono address + mono To; body 17. | Per spec (use MS01 avatar header only on single-message view). Body 16/25. | open |
| 13 | MS04 | Banner `.bnr`: `card` bg, r **12**, margin `8 16 6`, pad `12 14`, 15 pt, 18 pt "image-off" icon (secondary), **one line "Remote images are off for this sender."**, action **"Load images"** TA/500. Blocked images drawn as **dashed 1 pt `#BBB` boxes**, label `logo.png · blocked`, 11 pt `#888`, centred (heights 44 / 120). | Banner `grouped` bg r14, status **dot**, 2-line title "Images blocked" + message, 16 pt; WebKit broken-image alt boxes (`logo.png`, `product.jpg`) with top-left alt text. | Banner copy and layout per board. Replace broken-image alt with a CSS-injected placeholder (`img[data-blocked]` dashed box + `title · blocked`), or a content-rule that substitutes a data-URI SVG; keep nothing remote loading. | open |
| 14 | SR01, SR02 | Field h 38 r 10 fill `field`, **focus ring 1.5 pt accent**, accent caret, **Cancel 17 TA**. Scope control `seg` ("All businesses \| Acme Studio") shown under the field **whenever a business context exists** (SR02, SR03, SR04). Sections `Recent` / `Try` are **inset cards** (`card` fill, r 12, rows min 56): recent rows = **accent clock icon 22** + 16.5 text; Try rows = operator 16.5 (body font) with hint **below** in 14 secondary; `has:attachment` has no hint. | Field r≈12, h≈40, no ring, system blue caret; Cancel in `text`; no segmented scope (only a chip when scoped); sections are bare lists on ground with tertiary clock icons; operators in mono with hint in a right column; extra "Clear" action. | Per board. Caret/ring `accent`. Keep **Clear** (not on board; allowed, TA 15) — I add it to SR01 in the canvas (D2). Segmented control: `field` bg r 9 pad 2, selected `card` + shadow `0 1 3 rgba(0,0,0,.08)`, 14 pt, margin 0 16. | open |
| 15 | SR02 | Match highlight `.hl`: background **`#FCE2DA`** (dark: `#4A2220`), text unchanged (`text`), radius 3, pad 0 2. Section header `Acme Studio 2` sans 12.5 caps, count tracking 0. Sender/subject/preview as ML02 rows (sizes as row #7). | Matches in accent-coloured text (3:1, no background); headers mono 12, counts mono. | `AttributedString` run: `backgroundColor`, no foreground change. | open |
| 16 | Buttons, Foundations | Primary: h 52 r 14 fill AF text on-accent 17/500. **Secondary: transparent, 1.5 pt accent ring, TA text.** **Destructive: fill `#D92D20`, white text.** Plain: TA text. | Secondary = `grouped` fill, `text` colour; destructive = `grouped` fill, accent text; primary uses accent fill (3.04:1). | `PrimaryButton`: per spec; add `#D92D20` as `destructive` token (dark `#F0584A`, text `#1A0F16`). | open |

## P2

| # | Board | Design shows | App does | Fix |
|---|---|---|---|---|
| 17 | ML02 | Nav actions on Business page: search only. | Extra "filter" (lines-in-circle) icon beside search. | Remove (chips already filter) or tell design what it does. |
| 18 | InsetGroup | Radius **12**, row text 16.5, subtitle 14, hairline 1 pt from x=16. | Radius 14, 17, 0.5 pt. | Adjust. |
| 19 | SquareSwitch | Off fill `#CDBDA5` (dark `#4A3342`), knob fill `#FFFDF7` (dark `#F3EBD8`), shadow `0 1 2 rgba(0,0,0,.25)`; 48×28 r8, knob 22 r6, inset 3. | Off fill `hairline` (too light), knob = `card`/`onAccent`. | Token swap (`hairlineStrong`, `knob`). |
| 20 | Toast | r 12, pad 13/16, 15 pt, bottom 104 from screen bottom, left/right 20, Undo TA/500 (on `text` bg use `#FF5B45`-class, i.e. dark-accent). | r 14, 16 pt. | Adjust. |
| 21 | Banner | `card` bg r 12, pad `12 14`, 15 pt. | `grouped`, r 14, 16 pt. | Adjust (MS04 banner is P1 #13). |
| 22 | MailRow skeleton (ML09) | Bars h 11 r 5, widths 45/80/65 % of text column, gap 8, fill `hairline`. | 140/220/260 pt fixed, h 14/12/12, fill `grouped`. | Percent widths, h 11. |
| 23 | Body text | Plain message body 16/1.55. Links: TA underline. | 17; accent link 3:1. | Adjust; G1. |
| 24 | Search field | h 38, r 10. | ≈40 / 12. | Adjust. |
| 25 | Status bar etc. | — | Search shows "◀ Calendar" return pill in status bar in screenshots (sim artefact from launching via URL). | Re-shoot after cold launch. |

## Verified OK (no action)

Palette hex values (light + dark) match Foundations; unread dot 9–10 pt accent; sender weight 400/500 pattern; Mail/Rules/Settings tab set and accent top bar concept; swipe/undo/5 s model; "Sam (you)" thread rows; search operators set and recents/try content; Businesses copy (Unrouted / Failed subtitles).

---

# D3 gate log

## Gate 1 — Swift batch "fixes #1–#24 + T17" (reviewed from ios/docs light+dark shots)

**PASS (T11 businesses, T13 business page, T14 invoice / thread / images-blocked, T15 idle / results / scoped):** #1 (P0 closed: no tab bar, single 84 pt icon bar), #2 partial (fonts still system, accepted until D-005), #3–#16 closed. Banner wrapping to 2 lines on MS04 is accepted (board text also overflows one line at 390 pt).
Remaining P2, no gate impact: Businesses/Rules large title sits ≈10 pt higher than the board (board has a 44 pt nav row above the 32 pt title: title top = 96 pt); disabled `Edit`/prev-next chevrons must keep ≥ 40 % opacity so they stay visible.

**FAIL — T17 screens (new, NA02/NA04/NA05/RU01). Open P1s:**

| # | Board | Design shows | App does | Fix |
|---|---|---|---|---|
| 26 | NA05 | Nav: back **"Back"** + centred title **Failed**. Reason shown in a **banner** (`card`, r12, 18 pt `#D92D20` alert icon, one bold lead "**Couldn't store.**" + sentence, 15 pt). Actions are **inset rows**, centred 16.5 pt: card 1 = *Open in Gmail* (TA), *Retry* (TA/500?) rows; separate card 2 = *Dismiss* in `#D92D20` text. | Large 32 pt title "Too large to store." + subtitle (no banner); 3 **filled/outlined buttons**, Dismiss as solid destructive red fill; back label "Failed" duplicates the nav title. | Rebuild per board: banner + inset rows; Dismiss = destructive **text** row (not fill); back label = parent ("Failed" list → label **"Failed"** is acceptable only if the parent screen is titled Failed; board says "Back" because NA04 is the parent: use parent title). |
| 27 | NA05 copy | Use D2 spec: lead **"Couldn't store."**, "The server couldn't save this message. It was still delivered to Gmail."; log lines `received 4.2 MB … store raw → failed (storage unavailable)`; parse-failed variant hides Retry. Received format `Sep 12, 4:10 PM` (follow device locale 12/24 h). | Old board copy (31 MB / 25 MB limit). | `docs/design-specs/D2-gap-specs.md` §3. |
| 28 | NA02 | Nav: back "Businesses" only (no centred title); title **Unrouted** 32; subline **"3 messages no rule matched"**; footnote 13.5 secondary **"These are in Gmail and in their business as usual. Make a rule so mail like this is sorted next time."**; groups = `.sh` header + plain **MailRow** list (unread dot, 16/15/15, time) — not inset cards. | Duplicate title (nav + large); subline "3 messages"; different helper copy; rows in inset cards with "to hello@" line instead of subject+preview. | Per board. |
| 29 | RU01 | Nav: **Edit** only (right); title Rules 32; inline **search field**; one inset card of rows: name 16.5 + description 14 secondary ("invoices@ on acme-studio.com", "Any other address on acme-studio.com", "From @stripe.com on kettle.house"); switch right. No "+", no helper paragraph, no "→ label · N messages" third line. | Edit left + **+** right, helper paragraph, third line, rows are a bare list (no inset card, no search). | Per board; `Add rule` moves to Edit mode (RU02) — if the owner wants a persistent +, log Q-swift and I will add it to RU01. Description phrasing "X on domain" per board. |
| 30 | RU01 | "Import from Gmail filters" is a separate screen (RU04) entered from Edit/empty state, not a link in the list. | Accent link under list in `#F83D34` (3.0:1, violates G1). | Move into Edit mode; if kept, TA colour. |
| 31 | NA05/RU01 | Switch off fill `#CDBDA5`, knob `#FFFDF7`. | OK. | — |

**Verdict:** Batch 1 = PASS for T11/T13/T14/T15; **T17 = FAIL** until #26–#30 are fixed. NA04 (Failed list) has a board (NA04) — compare to it, not NA03/NA05 layout.

## Gate 2 — T21 Gmail batch (ST01, GM01, GM02, GM03, GM04, RU04; light reviewed in full, dark spot-checked: tokens correct)

Undesigned states (rate-limited banner, not-connected) are now specified in `docs/design-specs/D3-gmail-states.md`.

| # | Board | Design shows | App does | Fix | Sev |
|---|---|---|---|---|---|
| 32 | ST01 | Rows have a **leading accent icon** (22 pt) and a **value** at right: Gmail `sam@gmail.com`, Businesses `12`, Import older mail `90 days`, Alfather status ring, Health `All good` (secondary), Appearance `System`, About `1.0`. | Only Gmail shows a value; no icons; other values missing. | Add icons (as board SVGs) and values (secondary 16). Alfather ring = StatusDot-style 12 pt ring; Health value = "All good" / "Needs you". | P1 |
| 33 | GM01 | Account row: leading icon, name 16.5, subline with 8 pt **status dot** + "Connected · Read, label and archive". **Apply labels / Archive when rules say so are switches** (SquareSwitch). **Disconnect Gmail = inset row**, centred 16.5, `#D92D20` text (not a filled button). | No icon/dot; the two options render as "On" text; Disconnect is a solid red fill button. | Per board; switches write `/v1/prefs` (or disabled until wired, but visible). | P1 |
| 34 | GM03 | **Banner first** (icon + bold "Google signed Inbox out." + "14 label and archive actions are waiting and will run once you reconnect."), then account row with `● Disconnected` in the subline, **Reconnect Gmail** pinned at bottom 106. | Large 28-pt style heading + paragraph instead of banner; status at right of the row; button under the card; extra accent-text "Disconnect Gmail" link at the left. | Per board + D3 spec (Disconnect as separate destructive inset row). | P1 |
| 35 | GM02 | `.bnr` with 18 pt **icon** (not dot), bold lead and body **inline** in one paragraph, action "Reconnect" TA/500 right; sits between search and "All businesses". | Dot instead of icon; title and message on separate lines. | Banner component: icon variant, lead+body inline paragraph. Matches MS04 banner (#13). | P1 |
| 36 | GM04 | Account row (Connected), then one `.bnr`: bold "Catching up" + "9 of 14" secondary, progress bar below (6 pt, track `hairline`, fill accent). | Catching-up shown as a row inside the Sync card with extra rows below. | Accepted layout difference **if** bar = 6 pt r3 and "x of y" secondary; prefer the board banner. | P2 |
| 37 | RU04 | Nav right action **Import** (TA/500), no bottom button; rows have the **circular checkbox on the right** (22 pt; on = accent fill + `onAccent` check); 4th filter unchecked; Can't-import row title "from:(linkedin.com) → delete". | Square checkbox left, bottom "Import 4" button, all rows checked, no "→ delete". | Per board; showing the count in the nav action ("Import 3") is accepted. | P1 |
| 38 | ST01/GM01/RU04 | Title sits under a 44 pt nav row (title top 96 pt). | Settings title ≈10 pt high (same as #P2 earlier). | Same fix. | P2 |

**Verdict: T21 = FAIL (P1 #32–35, #37).** Dark mode tokens OK (accentText, accentFill `#FF5B45`, warning `#E0A03A`).

## Gate 3 — re-gate T17 #26–#30 and T21 #32–#37 (light reviewed in full)

**PASS:** NA05 failed-detail (banner + inset rows + new copy; log line wrapping accepted), RU04 import filters, GM01 connected, ST01 settings (blank Alfather/Health/Import values accepted until T24/T26 — never show made-up status), GM09-style rate-limited banner, not-connected screen, GM02 mail banner (icon + inline lead), GM04 catching-up (accepted layout), NA02 structure and copy.

**Open:**

| # | Screen | Problem | Fix | Sev |
|---|---|---|---|---|
| 39 | T21-gmail-signed-out-light.png | Wrong image: it is a **calendar app** screen ("October 2026", "Lunch with Sam"); the simulator left Inbox. GM03 is therefore unreviewed in light, and the dark shot predates the fix. | Re-launch Inbox cold and re-shoot light + dark. | blocker |
| 40 | RU01 Rules list | `List(.insetGrouped)` brings system metrics: title at x=36 (board 20), field/card inset 32–36 (board 16), card radius ≈22 (board 12), rows ≈86 pt tall (board 56), ≈100 pt gap between Edit and the title. | Browse mode: ScrollView + `InsetGroup` (16 margin, r12, row min-height 56, description 14 secondary, 2-line wrap allowed) + `PageTitle` (x=20). Use `List` only in Edit mode (RU02 drag handles). | P1 |
| 41 | NA02 Unrouted | Subject is shown twice per row (subject line and preview line identical). | Preview line = message snippet (first text), never the subject; hide if empty. | P1 |
| 42 | RU01 | Disabled rule ("Accounts") title is dimmed; board keeps full-contrast text, only the switch is off. | Do not dim row text. | P2 |

**Verdict:** T21 = PASS except GM03 (#39, awaiting a valid shot). T17 = PASS except RU01 (#40) and NA02 data line (#41). Do not ship RU01 or GM03 until re-shot.

## Gate 4 — T23 onboarding, T24 settings/health, T26 import (light reviewed in full; dark spot-checked on appearance + OB02: tokens correct)

**Closed since Gate 3:** #40 (Rules list now matches: x=20 title, 16 inset, r12), #39 (GM03 re-shot correctly), #41/#42 assumed fixed per message (NA02 not re-shot; will confirm with the next unrouted shot).

**PASS:** OB01 (SIWA footer ✓), OB02, OB05, OB08, OB09, OB10 (pairing copy ✓), BZ03/BZ06 structure, ST04 privacy copy ✓, AF01/AF02/AF04 copy ✓, GM06 setup, GM07/GM08 as built, first-open import banner.

**Open P1 (fix before ship):**

| # | Screen | Problem | Fix |
|---|---|---|---|
| 43 | ST03 Appearance | "Load remote images" row: the 2-line subtitle is clipped into the divider (row has no vertical padding for the 2nd line). | Rows grow to fit content: `padding 11 16`, min-height 56, subtitle 14 secondary may wrap. |
| 44 | ST04 Privacy | Alfather row subtitle wraps and touches the card's bottom edge (same cause). | Same fix. |
| 45 | BZ01 Businesses (settings) | Board: nav right = **+** icon (and Edit for reorder, BZ02), footnote "Order here is the order on the Mail tab. Tap Edit to reorder or hide.", and a **warning dot** (`#C27803`) at the right of any business with a routing problem. App: no dot (Lumen has a problem per Health), no footnote, "Add a business" as a list row. | Add dot + footnote; nav right = `Edit` and `+` (gap 18); keep "Add a business" row only if `+` is absent. |
| 46 | BZ03 Business detail | Label column too narrow: "Last message" wraps to two lines. Missing **Import older mail → "90 days imported"** row. Label 15 pt secondary, 92 pt column. | Per board; add the row (chevron to GM06). |
| 47 | GM08/GM09 copy | Paused banner reads "Paused by Gmail rate limits The import resumes…" (missing full stop). | "Paused by Gmail rate limits." |

**P2:** dates follow board rules (today → time; this week → weekday; older → "Oct 5", no year in the current year; "yesterday" for last-seen) — currently "Oct 5, 2026", "Oct 10, 2026 at 19:12"; thousands separators render "1.240" in the simulator locale (use `Locale.current`, but shoot docs in en_US so shots match boards); OB10 single-row card shows a stray bottom divider; onboarding card inset is 20 (board 16) and primary buttons 16; OB01 side padding 24 (check against the OB01 board when exported); Appearance uses a check glyph (board: 22 pt circle, `chk`); Health lists mail-arriving per business and orders Alfather before Gmail (board: one "Mail arriving" row, order Mail · Worker · Gmail · Queue · Alfather); BZ06 alert icon should be `#D92D20` (board), not warning orange; "Unpair Alfather" should be a destructive inset row (`#D92D20`), not accent text; Pause/Cancel import: Cancel is destructive; OB13/banner copy "Importing the last 90 days" (board); Rules title ≈30 pt lower than the board; OB08 shows Skip twice (top and bottom).

**Verdict: PASS except #43–#47.** Dark mode matches tokens in the two shots checked; please make sure dark shots for the fixed screens are re-shot with the next batch.

## Gate 5 — re-gate after reshoot (T17/T21 list, T23, T24, T26, T27-privacy)

Screenshots newer than Gate 4: appearance, privacy (19:19); settings, business-detail, routing-problem, alfather ×3 (19:10–19:12); import setup/running/paused/done and banner (19:13–19:14). `T24-businesses` (15:08) was **not** reshot; code changed at 19:17.

**Closed (verified in shots):** #43 Appearance row now fits; #44 Privacy Alfather row fits; #41 Unrouted rows show subject + snippet; earlier #26–#40 stay closed. T17 and T21 screens: no regressions against my earlier P0/P1 lists.
**Closed in code only (19:17), shot is stale:** #45 (footnote string present in `BusinessesScreens.swift:61`; warning dot and "+" not confirmed), #46 import row (`BusinessesScreens.swift:127`, `importSummary`), #47 (`ImportOlderModel.swift:96` now "Paused by Gmail rate limits.").

| # | Status | Needed |
|---|---|---|
| 45 | open until shot | Reshoot T24-businesses light+dark: must show the warning dot on a routing-problem business, the footnote, and Edit + "+". |
| 46 | open | T24-business-detail still shows "Last message" wrapped onto two lines and no "Import older mail" row. Reshoot after the 19:17 build; fix the label column (15 pt, 92 pt wide) if it still wraps. |
| 47 | open until shot | T26-import-paused still reads "…rate limits The import"; reshoot after the 19:17 build. |

**T27 privacy:** copy and layout PASS (`T24-privacy`); the delete-everything alert has no shot, built per `docs/design-specs/D3-gmail-states.md` — send a shot when available.

**Verdict:** PASS for T17, T21, T23, T26 (except #47 shot), T24 settings/Alfather/Appearance/Privacy/Health. **Not yet passable:** T24 Businesses (#45) and Business detail (#46). No P0 open. Remaining P2 list from Gate 4 stands.

## Gate 6 — reshoot of #45–#47 and the delete-everything sheet (light reviewed; dark not re-viewed this round)

**PASS:** #45 T24-businesses (warning dot on Lumen, footnote, Edit + "+"; the "Add a business" row duplicating "+" is accepted). #46 T24-business-detail (label one line, "Import older mail · Not imported" row). #47 T26-import-paused (full stop, title "Importing the last 90 days", en_US numbers "1,240" now match the boards). Destructive-colour rows (Unpair Alfather, Cancel import), OB08 single Skip and BZ06 error icon accepted per message (not re-shot individually).
**PASS with note — T27 delete-everything:** the typed-code confirmation sheet replaces my alert spec (stricter, accepted as a deliberate hardening; `D3-gmail-states.md` updated). Title 21 PolySans, Cancel in accentText, disabled button at 0.4 opacity of `#D92D20` all per tokens.
**P2s (no gate impact):** the code field shows the code itself in secondary text, so it reads as pre-filled — use the placeholder "Type the code" (mono 13 only for the typed value); date rule (today → time only, e.g. "7:26 PM", not "Oct 11, 2026 at …"); Business detail's last row is clipped by the tab bar until scrolled (add 24 pt bottom content inset); Appearance 22 pt circle, Health row order/per-business rows, onboarding card inset 16 (all skipped by Swift, still P2).

**Overall D3 status:** no P0/P1 open for T11–T15, T17, T21, T23, T24, T26, T27. Open: fonts (D-005, blocked on licence), P2 backlog above and in Gate 4, dark-mode re-check of the Gate 6 screens when convenient. NA02/RU01 dark shots predate fixes but share tokens.


## Gate 7 — dark-mode review of the latest round
Reviewed: T24 businesses, business-detail, settings; T26 import-paused; T27 delete-everything; T23 OB01 (dark).
**PASS, no P0/P1.** Tokens correct: accent `#FF5B45` for text and fills, `#1A0F16` text on accent buttons, warning `#E0A03A`, card `#24151F`, switch off/knob `hairlineStrong`/`#F3EBD8`, hairlines `#36222F`. Delete sheet disabled button is dim as intended.
P2: `T23-ob01-welcome-dark.png` is framed inside a rounded device shape with wallpaper edges (simulator was captured mid-transition), reshoot; delete sheet field/placeholder note from Gate 6 still applies; Business detail last row clipped by tab bar until scrolled.

## Gate 8 — Phase 2: S-1, S-5, S-6 (2026-10-12)
**S-1 Businesses edit:** FAIL — P1 row height/handle vs BZ02; P2 nav/footnote/tab bar. **S-5 AX3:** FAIL — P1 header avatar "…" and mid-word address wrap; P2 subline separator. **S-6 backlog:** PARTIAL — Health order, Appearance circle (dark), onboarding inset, OB01 dark closed; P1 relative dates not applied; P2 Appearance light shot stale. Details and exact fixes: `docs/handoff/design-to-swift.md`.

## D-3 — full-app visual walk (light + dark, mock mode; from the screenshots in ios/docs, not a fresh build)
| Area | Screens | Light | Dark | Notes |
|---|---|---|---|---|
| Mail | Businesses, Business page, All, Search idle/results/scoped | PASS | PASS | S7 shots show relative dates correct |
| Message | invoice, thread, images-blocked | PASS | PASS | AX3 header = P1 (Gate 8) |
| Needs attention | Unrouted, Failed list/detail | PASS | PASS | |
| Rules | list, editor (unrouted) | PASS | PASS | editor states RU02/06/07/09/10 have no shots |
| Gmail | connected, signed-out, not-connected, rate-limited, catching-up, banner, import filters | PASS | PASS | |
| Onboarding | OB01, 02, 05, 08, 09, 10 | PASS | PASS | OB03/04/06/07/11/12/13 have no shots |
| Settings | root, Businesses, detail, routing problem, Health ok/needs you, Alfather ×3, Appearance, Privacy, delete sheet | PASS | PASS | relative dates = P1 (Gate 8) |
| Import | setup, running, paused, done, Mail banner | PASS | PASS | |
| Businesses edit | BZ02 | FAIL (P1) | FAIL (P1) | Gate 8 |
| Message sheets | More, Details, Original, attachment viewer | no shot | no shot | not reviewed |
| Dev-server mode | `LIVE-businesses.png` only | PASS | – | single shot |
**Result:** 3 open P1 (S-1 rows, S-5 AX3 header, S-6 relative dates), no P0. Not covered for lack of shots: Message More/Details/Original/attachment, RU02/06/07/09/10, OB03/04/06/07/11–13, swipe/select/refresh states, dev-server mode beyond one shot.

## Gate 9 — re-gate of Gate 8 P1s (2026-10-12; light+dark shots of 13:00–13:03, screenshots only, no build run)
- **S-1 BZ02 rows: PASS.** 56 pt rows in one 12 pt card on the 16 pt gutter, centred "Businesses" title with Done, footnote above the list, tab bar hidden. P2: the system 3-line handle sits at the card edge and the row dividers stop short of it (board: 6-dot handle inside the card); accepted until a custom drag implementation.
- **S-5 AX3 message/thread header: PASS.** Avatar dropped at AX sizes, sender wraps, address and To use middle truncation, no mid-word breaks.
- **S-6 relative dates: PASS.** "Last seen yesterday, 1:03 PM", Business detail "Last message 1:02 PM"; Appearance light now has the 22 pt circle.
- **P2 left:** at AX3 the Business page subline wraps with the "·" at the start of the second line ("· 2 unread"); put the separator on the first line or drop it when wrapped.
**Result: no P0/P1 open.** D-2 re-gate complete.

## Gate 10 checklist — server sign-in (D-012) and Open Runde (D-011) (design agent, 2026-10-31)

Spec: `docs/design-specs/D4-signin-and-fonts.md`, tokens `tokens.md` v1.2. Swift: ship light + dark PNGs in `ios/docs/` named as below, then note it in `docs/handoff/swift-to-design.md`. Anything missing = open.

**A. Sign-in screen states** (light and dark each; names `G10-signin-<state>-<light|dark>.png`)
1. `welcome`: OB01 with footer "Next you'll enter your server address and password."; no Apple text, no spinner/banner; both buttons enabled.
2. `empty-new`: title "Connect to your server", button dimmed, focus ring on Server address, helper lines visible.
3. `ready`: both fields filled, password hidden, button full opacity. Plus `password-shown` (eye.slash icon).
4. `loading`: spinner in button, label hidden, fields dimmed.
5. `invalid-url`: inline TA error + ring on address row; also the `http://` public-host variant.
6. `wrong-password`: inline error under password, password cleared, address kept.
7. `locked`: warning Banner with `m:ss` countdown, button "Try again in m:ss" disabled; then at ≥10 min shows "about N minutes".
8. `unreachable`: Banner with Try again; TLS and 5xx wording variants checked in code or test.
9. `existing` and `signed-out` modes: title/caption per D4 §1.4; address prefilled in signed-out.
10. `ax3`: the empty and the locked states at AX3: button inside the scroll content, nothing clipped, title wraps, Banner action on its own line.
Pass checks: error text colour is `#BD251D` (light) / `#FF5B45` (dark), never `#D92D20`; rows 64 pt min, card r12 on the 16 gutter, button 52/r14 AF; keyboard types (URL / secure) and return keys (Next / Go) confirmed in a UI test; VoiceOver labels and hints match D4 §1.8 (a unit/UI test reading `accessibilityLabel` is enough); `grep -ri apple ios/InboxKit/Sources/FeatureOnboarding` returns nothing user-facing; Show password resets to hidden after backgrounding.

**B. Font swap snapshots** (names `G10-font-<screen>-<light|dark|ax3>.png`)
1. Font check: a test that `UIFont(name:)` resolves all four Open Runde faces and fails otherwise; `Info.plist` `UIAppFonts` lists them; OFL licence text shown in the app; no PolySans/Lausanne/GT America names left in code (`grep`).
2. Re-shoot at default size: Mail root, Business page, Message, Search, Needs attention, Rules, Settings, Health, OB01, OB02, sign-in. Compare type sizes to D4 §2.3 (spot-check: h1 32, title 30, sender 15.5, preview 14.5, time 13.5).
3. AX3 shots of the tight spots from D4 §2.4: mail row (sender vs date), chips, BZ02 edit row, message header (To/address middle truncation), settings value rows, caps section headers, badges, "Try again in 4:32" button.
4. Mono: NA05 log lines and any code field use system monospaced and wrap.
5. Digits: countdown and time column do not jitter (tabular figures).
Pass = no clipped or overlapping text at default and AX3, no mid-word breaks, no remaining system-font headings (tab bar and nav items excepted).

**C. Red text audit** (D-013): any red text on cream is `#BD251D`; `#F83D34` appears only on icons, dots, bars, switch fill, ring and textless fills; text-bearing fills use `#C8281F`. List any deviation by component.
