# design → swift (append-only, dated)

- 2026-10-12: D-1 done. New exports in docs/design-ref/ (BZ02, ML05/06, MS05, GM05, RU02/06/07/09/10, NA01/02/04/06/07, SR03/04, OB04/07/11/12). Read with `python3 -I docs/design-ref/extract.py <BOARD>`; README explains caveats. Specs in docs/design-specs/ override old board copy/colours.
- 2026-10-12: D-4 done: canvas page "Spec updates" (SU01–SU08) for reference; not needed to build. D-2 (gate S-1/S-5/S-6) and D-3 start when you send screenshots in ios/docs and note it in docs/handoff/swift-to-design.md.

## 2026-10-12 D-2 gate result (S-1, S-5, S-6) — 3 P1, 5 P2
- **P1 S-1 BZ02 rows:** edit-mode rows are ~98 pt tall (system List). Board BZ02: inset card (r12, margin 16), rows min 56, name 16.5 + domain 14 secondary, 48×28 square switch, then a 6-dot drag handle (`M9 7h.01M15 7h.01…`, 22 pt, `hairlineStrong`). Use InsetGroup rows + `.onMove`/drag handle, not List metrics.
- **P1 S-5 message header at AX3:** avatar initial renders as "…" (S5-ax3-message, -thread). At accessibility sizes drop the avatar's text (or hide the avatar), and make the sender/address/To lines `lineLimit(1)`+`.truncationMode(.middle)` (address currently breaks mid-word: "…example.co / m"). Same for the open card in a thread.
- **P1 S-6 relative dates:** T24-alfather-unreachable still shows "Last seen Oct 11, 2026 at 12:11 PM". Board/spec: "Last seen yesterday, 6:40 PM" (today → time only; yesterday → "yesterday, <time>"; this week → weekday; older → "Oct 5", no year in the current year). Same helper for Business detail "Last message" (currently "Oct 11, 2026 at 7:26 PM").
- P2 S-1: edit mode on the Mail root should match BZ02 (centred "Businesses" title in a 44 pt nav, "Done" right, footnote above the list, no tab bar); the app has a large title, footnote below, tab bar visible.
- P2 S-5: Business page AX3 subline wraps after "·" ("acme-studio.com ·" / "2 unread"): keep the separator on the second line; filter chips scroll horizontally (fine).
- P2 S-6: T24-appearance-light still shows the check glyph (dark has the 22 pt circle) — reshoot light.
- Passed: Health (one Mail-arriving row, order Mail·Worker·Gmail·Queue·Alfather), Appearance circle (dark), OB01 dark clean, OB02 card inset 16, S7 business page and Unrouted rules-editor shots.

## 2026-10-12 Gate 9: S-1, S-5, S-6 P1s all PASS (no P0/P1 open). Only P2s remain: BZ02 system drag handle edge, Business page AX3 subline "·" leading the 2nd line. Details in DESIGN_QA.md Gate 9.

## 2026-10-16 App icon ready
Asset catalog set at docs/design-specs/logo/AppIcon.appiconset/ (1024 px, no alpha, light/dark/tinted + Contents.json). To use: copy the folder into the app's Assets.xcassets (replacing the existing AppIcon set) and set ASSETCATALOG_COMPILER_APPICON_NAME=AppIcon. I did not touch ios/. Source SVGs: docs/design-specs/logo/cairn-1-night.svg, cairn-tinted.svg.

## 2026-10-31 D-4 (sign-in + fonts): what to build, in order
Spec: `docs/design-specs/D4-signin-and-fonts.md` (wins over D2 §1, which is void). Tokens: `tokens.md` v1.2. Gate list: `DESIGN_QA.md` Gate 10. Backend work needed first or mocked: `POST /v1/auth/login`, 401/429 + `Retry-After` (D4 §1.9).
1. **Remove Sign in with Apple** from OB01: no sign-in call, spinner, Banner or disabled state; the two buttons navigate with mode `new` / `existing`. New footer line "Next you'll enter your server address and password." Remove the Apple strings and `AppleSigning` use (grep "Apple" in FeatureOnboarding and the signed-out Banner).
2. **Build the Sign in screen** (D4 §1.2–1.4): two-row field card, URL + secure field with eye toggle, return keys Next/Go, address normalisation (https default, http only for localhost/.local/private IPs), Sign in button 52/AF, pinned footer (moves into the scroll at AX1+).
3. **States** (D4 §1.5): empty, ready, loading (15 s timeout), invalid URL, wrong password (clear password), locked out with live countdown from `Retry-After`, unreachable (+TLS/5xx wording), not-an-Inbox-server. Store device token in the Keychain and the URL in app storage; never the password.
4. **Wire the destinations**: `new` -> OB02; `existing` -> Mail root or resume at server `onboarding_step`; signed-out Banner "Sign in" -> same screen, address prefilled, password focused.
5. **Accessibility**: VoiceOver labels/hints/announcements per D4 §1.8, AX3 layout per §1.7.
6. **Red text** (D-013): switch all red text, including form errors and "Disconnect Gmail"/"Delete everything" text rows, to `accentText`; `destructive` only as button fill (tokens.md table).
7. **Fonts** (D-011): add the four Open Runde OTF/TTF files (convert from /fonts if needed; confirm PostScript names), `UIAppFonts` in Info.plist, OFL licence in Settings, update `InboxFont` (heading Bold/Semibold, body Regular/Medium, mono = system monospaced), then apply the size/tracking/line-height table (D4 §2.3). Add `monospacedDigit` on times, counts, countdown.
8. **Snapshots** for Gate 10 (A, B, C) in `ios/docs/`, then note in `swift-to-design.md`.
Owner decisions still open: see D4 notes in the report (attempts-left display, #F83D34 vs #C8281F for text-bearing fills, destructive text colour).

## 2026-10-31 Gate 11 list: CLOSED (H-1..H-17 all verified in Gate 12). History is in `docs/design-audit/GATE11.md`.

## 2026-10-31 Gate 12: ordered fix list (round D4). Audit: `docs/design-audit/GATE12.md`. New specs: `docs/design-specs/D6-gate12-decisions.md` (DD-n below refers to it)
Result: P0 0, P1 2, P2 24 (20 listed here, 4 left out). Gate does not pass until the two P1 are fixed. "Proof" = what a baseline in `ios/InboxTests/Snapshots` must show. Re-record only the baselines named; do not restyle anything not listed. Design decisions in D6 are the owner's to overrule; build them as written.

Left out on purpose (polish, do not build): NA06 retrying detail screen (DD-10), ML08 chips-with-no-mail case (DD-20 is a spec note, not a task), ML10 banner dot to icon (DD-21), GM06 radio glyph scaling at AX3, pull-to-refresh tint (DD-17), OB12 tracking -0.8 vs -0.68.

### P1
1. **F22 Delete-everything sheet disabled button (ST04b), DD-1.** While the code does not match, the button uses fill `field`, label `secondary` at full opacity (reuse the locked sign-in style); enabled state unchanged (destructive fill, white / #1A0F16 label). Proof: `ST04b-delete-sheet-light` and `-dark`: "Delete everything" readable (dark: light-grey label on #2C1B26, not a faint pink), and the enabled state shot (add `ST04b-delete-sheet-enabled-{light,dark}`) shows the red fill.
2. **F28 Sender HTML at Dynamic Type, DD-18.6.** Inject `-webkit-text-size-adjust: <pct>%` (pct = UIFontMetrics scale of the user's category against Large; 100 at Large, cap 200) into the HTML wrapper in the message web view. Do not touch dark/light tint handling. Proof: `AX3-MS01-message-{light,dark}`: the body text of "Your October invoice" is visibly the same order of size as the "Invoice #2041" subject (at least 3x its current height), no horizontal overflow; default-size `MS01-message` unchanged.

### P2, cheap (one line or a string each), in this order
3. **F21 App tint, DD-2.** Set the window tint to `accentText` (UIWindow `tintColor` or `.tint` at the root). Proof: `GM05-disconnect-alert-*`, `RU10-rule-delete-alert-*`: "Cancel" in red (#BD251D light / #FF5B45 dark), not blue; "Delete"/"Disconnect" stay the system destructive red.
4. **Copy batch, DD-8, DD-9, DD-13, DD-14.** NA01 subtitles "Mail no rule matched" and "Couldn't be processed"; Failed page subtitle "N message(s) couldn't be processed"; footnote "These were delivered to Gmail, but Inbox couldn't process them."; NA07 title "Nothing failed", message "Every message has been stored and sorted.", drop the "0 messages..." subtitle; RU06 sheet title "Gmail label"; RU07 error "Missing a closing bracket “)”." once, and remove the duplicate sentence under "Test against recent mail"; RU09 "No matches" / "None of the last 200 messages match. Check the address or widen the subject."; GM05 alert message "Labels and archive stop. Mail keeps arriving in Gmail and in Inbox." Proof: `NA01-needs-attention-root-light`, `NA04-failed-light`, `NA07-failed-empty-light`, `RU06-label-picker-light`, `RU07-rule-editor-invalid-light` (error appears once), `RU09-rule-test-none-light`, `GM05-disconnect-alert-light` show the exact strings.
5. **F12 Archive toast, DD-11.** "Archived · also in Gmail" when Gmail write-back is connected, plain "Archived" otherwise. Proof: `ML06-archived-toast-light` shows the long form (mock has Gmail connected); a unit test covers the disconnected case.
6. **F5 Mail root order, DD-8.** All businesses row above the Needs attention group. Proof: `NA01-needs-attention-root-light`: order Search, All businesses, NEEDS ATTENTION, N BUSINESSES. `AX3-NA01` still has no mid-word breaks.
7. **F19 Harness: AF03 must show Reconnecting, DD-16.** Make the AF03 baseline use last-seen 5 to 30 minutes ago. Proof: `AF03-alfather-reconnecting-*` title "Reconnecting...", subtitle "Alfather will pick up where it left off."; AF04 unchanged.
8. **F32 Harness: BZ01 normal state.** Add `GET /v1/businesses` to the mock server (or seed the repository) so the shot has no "Couldn't refresh" banner. Proof: `BZ01-businesses-settings-*` without the banner.
9. **AX3 batch, DD-18.1 to 18.5 (F23 to F27).** (a) leading glyphs in InsetRow/Mail-root rows use `@ScaledMetric` (min 22 pt); (b) search Cancel capped at `.xxxLarge` at AX; (c) value rows leading-align multi-line values; (d) Gmail account row: hide the envelope at AX1+, allow scale to 0.75 before truncation; (e) Health rows min 12 pt vertical padding. Proof: `AX3-ML01-businesses-light` tray glyph is the same height as the title's x-height or larger; `AX3-SR02-search-results-light` field shows at least "invoice" (8 characters); `AX3-MS07-details-light` "98 KB · 1 attachment" left-aligned under its caption; `AX3-GM04-gmail-catching-up-light` e-mail "sam@gmail.com" with at most a 0.75 scale, no ellipsis; `AX3-ST06-health-issues-light` visible gap between rows (at least 12 pt above and below each).
10. **F29 Back label collapse, DD-19.** Pushed bars with centred title show "Back" when label and title are closer than 12 pt. Proof: `BZ04-business-detail-routing-light` (title "Lumen Hosting") shows "‹ Back" with at least 12 pt before the title; add one baseline with a 24-character business name.

### P2, medium (do last; skip any that is not done in the round and say so)
11. **F7 NA04 grouping, DD-9.** Group the Failed list by business with a caps header "ACME STUDIO · 1" (same component as NA02); row preview is the failure title. Proof: `NA04-failed-light`.
12. **F13 RU02 layout, DD-12.** Subtitle "Order is priority: the first rule that matches wins. Drag to reorder." under the title (remove the footer); title at the normal position; rows min 56 pt. Proof: `RU02-rules-edit-light`: the first rule row is no more than 64 pt tall (single-line subtitle), title not inset by 36 pt, red minus and handle still present.
13. **F3 OB11 structure, DD-7.** Skip top right; caption "Inbox works fully without it. Mail just won't be read in the background yet."; row "Alfather · Not reachable"; two-item checklist; hint "You can pair later in Settings → Alfather."; buttons "Try again" (primary) and "Set up later". Proof: `OB11-alfather-not-found-light` and `-dark`, plus `AX3` if the screen wraps.

After this round re-run the snapshot suite and send the list of re-recorded names in `docs/handoff/swift-to-design.md`; design will re-check items 1, 2, 3 and 9 and mark the gate.
