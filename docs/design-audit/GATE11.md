# Gate 11: full screen audit (design worker, 2026-10-31)

Method: every board in `docs/design-ref` (copy via `extract.py`, geometry via specs D2/D3/D4/tokens v1.2, specs win, D-017) against the app PNGs in `ios/InboxTests/Snapshots` (recorded 10-31 12:15 to 13:22, Open Runde era) and `ios/docs` (`G10-*` current; `T*`/`S*` from 10-11/12, **system font era, stale for type**), plus a read of `ios/InboxKit/Sources` for tokens and sizes. No build was run, no code touched. Status: match / P2 / P1 / P0. "Unverifiable" = no current snapshot, counted as a finding.

Severity totals (merged into handoff items H-1..H-17): **P0 0, P1 8, P2 9** (plus a list of unverifiable screens, section 6).

## 1. Red-text audit (Gate 10 C, D-013/C-18)

Rule: red text = `accentText` (#BD251D / dark #FF5B45); `accent` (#F83D34) only icons, dots, bars, rings, switch fill, textless fills; `accentFill` (#C8281F) for fills carrying text; `destructive` (#D92D20) only as a button fill with white text.

| Where | Code | Resolves to | Verdict |
|---|---|---|---|
| Back, Cancel, Done, Fix, Try again, banner action, sheet Done, links, search Cancel | `accentText` in Navigation/Components/Buttons | #BD251D | match |
| Primary button, selected chip, sign-in button, check circles | `accentFill` (Buttons:39, Components:39, SignInScreen:211, ImportFilters:86, Settings:74) | #C8281F | match |
| Switch on, unread dot, tab icon + bar, progress, rings, status error dot, radio circle | `accent` | #F83D34 | match (non-text) |
| Sign-in inline error + ring | TA text, `accent` ring | match |
| **"Disconnect Gmail" text row** GmailScreen:137 | `p.destructive` | #D92D20 text, 3.99:1 | **P1** use `accentText` |
| **"Delete everything Inbox stored"** PrivacyScreen:96 | `p.destructive` | same | **P1** |
| **"Cancel import"** ImportOlderScreen:131 | `p.destructive` | same | **P1** |
| **"Remove business"** BusinessesScreens:178 | `p.destructive` | same | **P1** |
| **"Unpair Alfather"** SettingsScreens:182 | `p.destructive` | same | **P1** |
| **"Dismiss"** AttentionScreens:204 (`action(... color: p.destructive)`) | destructive | same | **P1** |
| **Rule editor errors** RuleEditorScreen:43 (subject text), :63 (`why`), RulesEditor Support.swift:32 (RuleFieldRow error) | destructive | same | **P1** use `accentText` |
| **Attention error text** AttentionScreens:91 | `p.accent` as text | #F83D34, 3.04:1 | **P1** use `accentText` |
| **Details "fail" values** DetailsScreens:121 (`p.accent` text for non-pass) | accent text | 3.04:1 | **P1** use `accentText` |
| Banner `.error` tint Components:175 (icon only) | destructive | icon | P2: use `accent` for icons (D-013 table); dark nearly identical |
| Delete-everything confirm button, Delete rule button | `.destructive` fill, white text | #D92D20 / white 5.0 | match |
| Dark mode | `accentText` = `destructive` ≈ #FF5B45/#F0584A | indistinguishable | no dark issue |

All of the above are one fix ("red text audit", H-1 in the handoff). Evidence in shots: `GM03-gmail-signed-out-light` and `ST04-privacy-light` show the orange-red #D92D20 text next to the #BD251D back label.

## 2. Open Runde type audit (Gate 10 B)

Code facts: `InboxFont.heading` = Semibold, `inboxHeading` is Semibold for **every** heading; `inboxHeadingBold` (Bold) used only by sign-in title and OB01 h1. Size histogram of `inboxBody(`: 15 x47, 14 x43, 16 x32, 17 x24, 16.5 x23, 13.5 x14, 12.5 x5, 13 x4, 14.5 x1, 11 x1. The D4 §2.3 sizes (15.5, 14.5, 13.5, 13, 12) are almost unused: **the table is not applied** (Swift said so). `monospacedDigit` appears once in the whole app. Letter tracking is applied on 6 headings only.

| Style (D4 §2.3) | Spec | Code now | Status | Fix location |
|---|---|---|---|---|
| Onboarding h1 | Bold 32/36, -0.8 | `inboxHeadingBold(32)` OnboardingScreens:112, tracking set (-0.4?) check | P2 | verify tracking -0.8 and 36 pt line height (code sets a tracking value on this line; confirm it is -0.8) |
| Title (nav large) | Bold 30/34, -0.75 | `inboxHeading(32)` Semibold, -0.64 (Navigation:12) | P1 | `inboxHeadingBold(30, .largeTitle)`, tracking -0.75 |
| h2 (step titles) | Bold 27/31, -0.5 | Semibold 28, -0.4 (Onboarding:84); Gmail 28 (GmailScreen:61); RuleTest 28; Settings 28 (:161, :208); sign-in 27 Bold OK | P1 | `inboxHeadingBold(27, .title2)`, -0.5 |
| ImportOlder title | h2 | 24 (ImportOlderScreen:109) | P1 | h2 style |
| Subject (message) | Semibold 20/25, -0.2 | Semibold 21 (MessageScreen:74,163) | P2 | 20 |
| Sheet title | Semibold 20/24 | 21 (Buttons:60), 22 (Components:216 empty state), 20 (MoreSheet ok) | P2 | 20 |
| Row sender | 15.5 Regular/Medium | 16 (MailRow:61,66; MessageScreen:146,183) | P1 | 15.5 |
| Row title / list primary | Regular 16 / 20 | 16.5 (Components:110 InsetRow, Businesses rows, Settings) | P1 | 16 |
| Row subtitle/domain | 13.5 | 14 (Components:111, Banner message) | P1 | 13.5 |
| Subject line in list | 14.5 | 15 (MailRow:71) | P1 | 14.5 |
| Preview | 14.5 | 15 (MailRow:72) | P1 | 14.5 |
| Time/date | 13.5 `monospacedDigit` | 14, no tabular digits (MailRow:62,68; MessageScreen:148,186) | P1 | 13.5 + `.monospacedDigit()`, time column minWidth +4 |
| Section header caps | Medium 12, +0.05em (0.6) | 12.5, tracking 0.5 (Components:86, MessageListScreen:39, AttentionScreens:25, SearchScreen:208) | P2 | 12 / 0.6, allow 2 lines at AX |
| Button | Medium 17 | 17 Medium | match | |
| Body message | 16/1.5 | 16 lineSpacing 5 (≈1.5) | match | |
| Caption/onboarding body | 14.5/1.45; OB01 intro 16.5 | 15/16/17 | P2 | per table |
| Footnote/helper | 13/1.4 | 13.5 helper (SignIn) | P2 | 13 |
| Chip | Medium 13.5 | 14.5 (Components:36) | P1 | 13.5 (height 32 kept) |
| Nav title centred | Semibold 16.5 | system nav title (black, see P2 below) | P2 | `.principal` Text 16.5 semibold `text` |
| Back/Cancel/links | Medium 17 | Regular 17 (Navigation:41, :64) | P2 | medium |
| Banner text | 14.5, lead Medium | 15 / 14 (Components:184-189) | P1 | 14.5 |
| Field label | Medium 13.5 | check SignInScreen (renders as 13.5 medium) | match | |
| Badge/count | Semibold 12 `monospacedDigit` | counts 16 Regular secondary in rows (BusinessesView:122) | P2 | `monospacedDigit`; row counts are 16 per ML01 board, keep |
| Tab labels | system | custom `inboxBody(11, medium)` TabBar:46 | P2 | board `.tab` says 11/500; D4 says system chrome unchanged. Spec conflict, keep 11 Open Runde (matches board); note in D5 |
| Mono | system mono 12 | `inboxMono` OK | match | |

Truncation risks at AX3 (D4 §2.4), checked in shots:
1. Mail row sender/date at AX3: no snapshot of the mail list at AX3 (only Businesses root). Unverifiable.
2. Large title: `AX3-ML01-businesses-light` breaks **"Businesse/s"** mid-word and "Edit" collides on the title line. **P1** (D4 §2.4 #2: lineLimit(2), no mid-word). Fix: put "Edit" on its own row at AX1+, title full width, `.lineLimit(2)` + `.minimumScaleFactor(1)` and allow `.fixedSize(vertical)`; if a single word still exceeds width, reduce title to 30 at AX (the 30 pt change in H-3 helps).
3. Chips: unverified (no AX3 Business page shot after font swap; stale S5 shots exist).
4. BZ02 AX3: `AX3-BZ02` shot exists but only light Businesses at default were viewed; treat as unverified for domain middle-truncation.
5. Message header AX3: `S5-ax3-message` stale (system font), logic present (middle truncation). Unverifiable after font swap.
6. Settings value rows AX3 (`AX3-ST01`), caps headers, badges: shots exist, not showing wrapped values beyond fit; status P2 unverified.
7. "Try again in 4:32" at AX3: fits on one line, passes; but dimmed to illegibility (see sign-in P1).
8. Section headers at AX3 allow 2 lines: confirm `lineLimit(nil)` (code does not set one; ok).

## 3. Screen-by-screen table

Sizes are 390 x 844 pt. "L/D/AX3" shows which current shots exist.

### Onboarding and sign-in

| Screen (board) | Shots | Status | Difference | Fix |
|---|---|---|---|---|
| OB01 Welcome | L D AX3 | P2 | Footer line copy matches D4. No Apple text. Spacing matches. h1 weight/tracking per table; AX3 stacks fine, "I already set this up" wraps at AX3 (OK, spec allows). | h1 tracking -0.8 (type table) |
| OB01b Sign in empty-new/ready/password-shown/loading/invalid-url(+http)/wrong-password/not-inbox/existing/signed-out/unreachable(+tls,5xx) | L D (+AX3 empty, wrong-pw, unreachable) | match (layout, copy, red tokens, ring, 64 pt rows, 52/r14 AF button) | Secure field renders blank in snapshots (no dots), so "ready" cannot prove the password is filled; focus ring/focus order unverifiable in static render. | use a snapshot mode that shows 13 bullets, or show the `password-shown` shot as proof (exists, OK) |
| OB01b locked / locked-long | L D AX3 | **P1** | Button label "Try again in 4:32"/"Try again later" at opacity .4 on #C8281F is about 1.5:1 (light), nearly invisible in dark (`G10-signin-locked-dark`). The Banner already carries the countdown, but the label is the spec'd state. | see D5 proposal: disabled fill at .4 but label stays `onAccent` at 1.0 opacity, or use `secondary` label on `field` fill for the locked state. `SignInScreen` button around line 205-215. Needs owner OK (spec change) |
| OB01b empty/disabled button | L D | match | Dimmed .4 per spec (labels low contrast is spec-conform; disabled exempt). | none |
| OB01b AX3 empty / locked / unreachable | L D | match | Button in scroll, Banner action on own line, title wraps, address truncates with ellipsis (spec says scrolls; acceptable in static render). | none |
| OB02 Domains | L D AX3? (L, D only) | P2 | Board shows "Continue with 3 businesses", app 4: data difference, fine. Layout match. Footer button is cut off at the bottom of the snapshot (no home-indicator inset in the harness). | wrap in `DeviceFrame` in `SnapshotTests` (harness only) |
| OB03 Route domain | none | unverifiable P1 | Screen exists in code (`DomainsStep` detail), no shot | add snapshot |
| OB04 Domains after fix | none | unverifiable | no shot | add |
| OB05 Connect Gmail | L D | P2 | matches copy; button clipped by harness as OB02 | harness inset |
| OB06 Continue in browser | none | unverifiable | text exists in code | add |
| OB07 Gmail connected | none | **P1** | board copy "Gmail connected / Read, label and archive / disconnect any time" not found in `FeatureOnboarding` sources; no screen | owner decision: build OB07 or document skipping; if built, per board |
| OB08 Filters | `T23-ob08` (stale font), L D | P2 unverified in Open Runde | reshoot | add `OB08-filters` baseline |
| OB09 Backfill | L D | P2 | Counts render "1.100 / 3.200 / 12.800" (grouping separator from the simulator locale) vs board "1,100". Spec does not set locale; real devices follow the user locale which is correct. | pin `en_US` in snapshot harness; no app change |
| OB10 Alfather found | L D | P2 | single-row card shows a stray hairline under the row (card bottom edge at y≈300); copy follows D2 pairing copy (spec wins over board). | remove the divider when the group has one row |
| OB11 Alfather not found | none | unverifiable | text in code | add |
| OB12 "You're set up" | none | **P1** | text "You're set up." / "Open Inbox" not found in source | owner decision (see OB07) |
| OB13 Mail root with import banner | `T26-mail-import-banner` stale | unverifiable (font) | | reshoot |

### Mail

| Screen | Shots | Status | Difference | Fix |
|---|---|---|---|---|
| ML01 Businesses root | L D AX3 | P1 (type) / P1 (AX3) | Layout matches board (search field, header, rows). Sender-size table not applied (16.5 rows). AX3: title breaks mid-word, Edit collides, counts and chevron wrap beneath titles (P2). | H-3, H-5 |
| ML02 Business page | S7 L D (stale) | P1 unverified | Layout was PASS at Gate 9; type sizes pre-date D4; chips 14.5 not 13.5; sender 16, preview 15 | type table, reshoot `S7-business-page` |
| ML02 AX3 Business page | S5 stale | P2 | subline "· 2 unread" separator (Gate 9 leftover) | keep "·" on first line |
| ML05–ML07 (swipe, select, refresh states) | none | unverifiable | | add snapshots |
| BZ02 Businesses edit | L D AX3 | P2 | 56 pt rows, 12 card, footnote above, centred title, Done: match. System 3-line handle at the card edge, dividers stop short (accepted P2 since Gate 9). Nav title is `text`-coloured medium: match. | custom 6-dot handle later |
| MS01/03/04 Message, thread, images blocked | T14 L D (stale) | P1 unverified | Structure PASS earlier; Open Runde sizes (subject 20, sender 15.5, time 13.5) not applied | type table, reshoot |
| MS01 AX3 | S5 stale | P2 | header logic present; recheck after font swap | reshoot AX3 message + thread |
| MS05 attachment, MS06 More, MS07 Details, MS08 Original, MS09 | none | unverifiable | screens exist in code (MoreSheet, DetailsScreens) | add snapshots |
| SR01-SR04 Search idle/results/scoped, no results | T15 L D (stale) | P2 unverified | match earlier; SR03 no-results state has no shot | add `SR04-no-results` |

### Needs attention, Rules

| Screen | Shots | Status | Difference | Fix |
|---|---|---|---|---|
| NA01 Needs attention list, NA02 unrouted, NA03/04 failed list/detail | T17 L D (stale) | P1 | Red text issues: Dismiss row, error text (section 1) | H-1 |
| NA05 Failed detail / log | T17 failed-detail | P2 | mono log lines wrap, OK | |
| NA06/NA07 | none | unverifiable | | add |
| RU01 Rules list | L D AX3 | match (no board exported, spec RU) | | |
| RU02, RU05-RU10 editor states | S7 unrouted only | unverifiable | RU02/06/07/09/10 no shots; editor errors use destructive (H-1) | add snapshots |
| RU04 Import filters | L D | match | Board RU04 exported; app title "Import from Gmail", action "Import 4": copy per spec | |

### Gmail, Settings

| Screen | Shots | Status | Difference | Fix |
|---|---|---|---|---|
| GM01/GM05 connected + disconnect alert | `T21-gmail-connected` stale | P1 | "Disconnect Gmail" red text row destructive; alert unshot | H-1; shoot GM05 alert |
| GM02 Mail banner Gmail disconnected | `T21-mail-signed-out-banner` stale | unverifiable (font) | | reshoot |
| GM03 signed out | L D AX3 | P1 | red row token; nav title "Gmail" renders black not `text` (P2); Reconnect button clipped (harness) | H-1, H-9 |
| GM00 not connected | L D | P2 | nav title black; harness clip | |
| GM04 catching up, rate-limited | T21 stale | unverifiable (font) | | reshoot |
| GM06 Import older | T26 L D stale | P2 | radio circles use accent (non-text, ok) | reshoot |
| ST01 Settings | L D AX3 | match | | |
| ST03 Appearance | L D | P2 | nav title black | H-9 |
| ST04 Privacy | L D | P1 | delete row destructive text | H-1 |
| ST06 Delete-everything sheet | `T27` stale | P1 unverified | typed-code sheet; destructive button fill OK | reshoot |
| Health, Alfather x3, Business detail, Routing problem | T24 stale | unverifiable (font) | Unpair Alfather red text (H-1) | reshoot |
| AF01-AF04 (Alfather) | T24 | as above | | |

### Global/cross-cutting

| Item | Status | Detail |
|---|---|---|
| Tab bar | match | icon+11 pt label, 30x3 bar, accent icon/TA label per code |
| Nav chrome | P2 | pushed screens use system `navigationTitle` which renders pure black (GM03, ST03, ST04, RU04 shots) instead of `text` #461634 / dark #F3EBD8 (dark shows white). Fix in `PushedChrome` (Navigation.swift:75): `.toolbar { ToolbarItem(.principal) { Text(title).font(.inboxHeading(16.5)).foregroundStyle(p.text) } }` |
| Dark mode tokens | match | sign-in, GM03 dark checked: ground/card/text/accent tokens correct |
| Snapshot coverage | P1 process | Only 21 screens have Open Runde baselines; about 40 screens exist only as pre-font shots or not at all (section 6) |

## 4. D2/D3/D4 consistency notes
- D4 §2.3 lists tab bar labels as system chrome but the board and tokens.md (tab 11/500) draw Open Runde labels; app uses Open Runde 11 Medium. Left as is, proposal D5 §1 confirms.
- D4 sign-in "locked" disabled label contrast is a spec gap (D5 proposal §2, needs owner).
- D2 §1 (Apple) is void as D4 says.

## 5. Count
P0: 0. P1: 8 (H-1 red text tokens, H-2 type sizes, H-3 heading weights/tracking, H-4 tabular digits, H-5 AX3 large title, H-6 locked button label, H-7 OB07/OB12 missing, H-8 snapshot coverage). P2: 9 (H-9..H-17). Ordered list with acceptance criteria: `docs/handoff/design-to-swift.md`.

## 6. Unverifiable (no current Open Runde snapshot)
OB03, OB04, OB06, OB07, OB08 (stale), OB11, OB12, OB13; ML05-ML07 states; MS01/03/04 (stale), MS05-MS09 (More, Details, Original, attachment); SR01-SR04 (stale); NA01-NA07 (stale); RU02/05-RU10; GM01, GM02, GM04, GM05 alert, GM06; ST06 sheet; Health; Alfather AF01-AF04; Business detail; routing problem; Import states (setup/running/paused/done); Mail root banners; Business page and every list/message screen at AX3 after the font swap; secure-field dots in the sign-in snapshots; focus ring and VoiceOver behaviour (not provable by static PNG).
