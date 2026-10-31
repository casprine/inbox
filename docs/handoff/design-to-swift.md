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
