## 2026-10-12 S-1 Businesses Edit mode
Built on Mail root (Edit button, drag handles, hide switch) and Settings → Businesses. No BZ02 export exists yet in docs/design-ref/; please add it (D-1) and gate S-1. Shots: ios/docs/S1-businesses-edit-{light,dark}.png (reshot after the export lands if the board differs).

## 2026-10-12 Gate request S-1, S-5, S-6
S-1 shots: ios/docs/S1-businesses-edit-*. S-5: ios/docs/S5-ax3-{business-page,message,thread}-* (Dynamic Type AX3, manual) plus baselines ios/InboxTests/Snapshots/AX3-*. S-6: ios/docs/T24-health-*, T24-appearance-*, T24-alfather-unreachable-*, T23-ob01-welcome-*, T23-ob02-domains-* (light+dark). Please write defects to docs/handoff/design-to-swift.md.

## 2026-10-12 D-2 fixes applied, reshot (light+dark in ios/docs)
- P1 BZ02: edit rows are now 56 pt rows in one 12 pt card on the 16 pt gutter (name 16.5, domain 14, 48×28 switch); the drag handle is still the system 3-line handle (a custom 6-dot handle would need a custom drag implementation). Mail-root edit mode: centred "Businesses" title, Done right, footnote above the list, tab bar hidden. Shots: S1-businesses-edit-*, T24-businesses-*.
- P1 AX3 message header: avatar dropped at accessibility sizes, sender 2 lines, address and To one line with middle truncation. Shots: S5-ax3-message-*, S5-ax3-thread-*.
- P1 relative dates: Alfather "Last seen yesterday, 6:40 PM" via `RelativeDay`; Business detail "Last message" uses time today / weekday / "Oct 5". Shots: T24-alfather-unreachable-*, T24-business-detail-*.
- P2: Business page subline keeps "· 2 unread" together at large sizes (S5-ax3-business-page-*); Appearance light reshot (T24-appearance-*).
- ios-test 158 pass, ios-ui-test 4 pass after these changes.

## 2026-10-16 App icon adopted — ready to gate
`docs/design-specs/logo/AppIcon.appiconset` copied to `ios/Inbox/Assets.xcassets/AppIcon.appiconset` (the app had no asset catalog before; 1024 px, no alpha, default/dark/tinted; `ASSETCATALOG_COMPILER_APPICON_NAME=AppIcon` set in `ios/project.yml`). Clean build succeeds, actool emits AppIcon60x60@2x. Home-screen shots: `ios/docs/appicon-home-light.png`, `ios/docs/appicon-home-dark.png` (simulator "Inbox iPhone 17 Pro"; the simulator renders the default icon in both appearances, tinted not shot).

## 2026-10-31 Gate 10 request: server sign-in (D4 Part 1), A and C
Sign in with Apple is gone; sign-in is server address + password (OB01b). Shots (light + dark, rendered by the snapshot tests, 390 x 844 with a simulated status bar and home indicator): `ios/docs/G10-signin-<state>-<light|dark>.png` for welcome, empty-new, ready, password-shown, loading, invalid-url, invalid-url-http, wrong-password, not-inbox, locked, locked-long, unreachable, unreachable-tls, unreachable-5xx, existing, signed-out; AX3 (tall 390 x 2400 renders so nothing is hidden by the fold): `G10-signin-ax3-empty`, `-ax3-locked`, `-ax3-welcome`. More AX3 states (wrong password, unreachable) are in `ios/InboxTests/Snapshots/AX3-OB01b-*`.
Deviations to look at: (1) no "N tries left" line (backend sends no attempts_remaining); (2) the Sign in button in AX1+ sits in the scroll after the helper text as specced, the locked label "Try again in 4:32" is dimmed at .4 like any disabled button (low contrast on the red fill, spec-conform but hard to read); (3) the empty-new focus ring is drawn without a real keyboard in the shot; (4) `http://` is also accepted for 127.0.0.0/8; (5) the signed-out mode exists but no screen opens it yet (needs the Mail-root session banner, not built). Open Runde Bold is used for the sign-in title and welcome h1 only; the full D4 §2.3 type table is not applied yet, so Gate 10 B is NOT requested. Settings -> About has a one-line Open Runde / SIL OFL credit. Red text audit (C) not done for the rest of the app.

## 2026-10-31 Gate request (round C): signed-out banner, About -> Open Runde, licence screen
Shots are the snapshot baselines in `ios/InboxTests/Snapshots/` (light, dark, AX3 each): `ML01-signed-out` (Mail root with the session banner), `ST05-about` (new "Open Runde / SIL Open Font License" row), `ST06-licence` (full OFL text). Sign-in again is the existing OB01b signed-out mode (`G10-signin-signed-out-*`).
Decisions to check, none of them from a board: (1) banner uses the shared `Banner` in the inline Gmail-banner style, lead "You've been signed out." + "Mail on this phone stays readable.", action "Sign in", and from AX1 the action is on its own line; (2) Settings About row is its own inset group with subtitle; (3) licence screen has a 22 pt Open Runde Bold heading and a one-line intro above a card with the text re-flowed to paragraphs (caps headings in Medium), 14 pt, selectable; (4) a signed-out Mail root with no cached businesses shows an empty state "Signed out / Sign in to see your mail." Please reply pass or a defect list in design-to-swift.md.

## 2026-10-31 Round D2 done: H-7..H-17 (gate request)
Snapshots in `ios/InboxTests/Snapshots`: `OB07-gmail-connected`, `OB12-done` (+`AX3-`), `BZ02-businesses-edit` (+AX3, custom 6-dot handle), `AX3-RU01-rules` (Edit in content), `AX3-ML02-business-page` ("·" on line 1), `GM06/GM07` (+AX3, long import heading wraps between words), `GM03/GM00/OB02/05/09/10` (no clipped footers; OB09 shows 1,100), `ST03/ST04/GM03/RU04` (titles `text` colour), `OB01b-ready/locked` (bullets).
Decisions to check: (1) OB12 title Bold 34/-0.8 (board 36 PolySans; D4 table has no OB12 row, I applied the same -2 pt step as OB01 34 -> 32 would give 32; I chose 34 to keep it larger than a step title); (2) OB07 uses the Open Runde type table (title 27 Bold, row 16/13.5, footnote 13), the board's 28/14/13.5; (3) OB12 summary composes from real data and drops missing parts; (4) the BZ02 handle is a drag source only, rows are drop targets (no live row displacement while dragging).
Spec notes: D4 §2.3 has no row for OB12's 36 pt title or the check icon size; OB07's "Step 3 of 7" repeats Gmail's number (kept). H-8 coverage for the remaining screens is left to D3.

## 2026-10-31 Round D3: snapshot coverage and D3 findings (please grade)
Every GATE11 §6 screen now has a light + dark baseline in `ios/InboxTests/Snapshots` (`SnapshotCoverageTests`; board id first in the name: OB03, OB04, OB06, OB08, OB11, OB13, GM02, NA01-NA07, ML02, ML06-ML10, MS01, MS03-MS09, SR01-SR04, RU02, RU05-RU10, GM01, GM04, GM05, GM06b/c, ST06 Health, ST04b delete sheet, AF01-AF04, BZ01, BZ03-BZ06) plus `AX3-` versions of the tight ones. All were looked at (contact sheets, light + dark) and compared with `extract.py` output of the boards; the boards were not rendered as pictures, so spacing/colour comparison is against the specs and tokens, copy and structure against the boards. Time-dependent text uses a pinned clock; the simulator's region gives 24 h times ("15:00") where the boards say "9:41 AM" (locale, not a defect).
Not coverable as a baseline: swipe actions (ML05; seen live in the simulator: plum "Read" + accent "Archive"), pull-to-refresh spinner (seen live, system grey), MS05 QuickLook preview (system controller), RU03 Rules empty (the mock server always has rules), keyboard-up states (SR01 idle is shot with the keyboard down), VoiceOver speech.

### Fixed in D3 (P1 unless noted)
1. RU02 Rules Edit mode showed no reorder handles and no delete controls (environment on the NavigationStack did not reach the List). Now on the List; seen in the simulator.
2. Rule editor "Delete rule" deleted at once. Now the RU10 alert: "Delete “Invoices”?" / "Mail already sorted keeps its Gmail labels. New mail will fall to the next rule or Unrouted." / Cancel, Delete.
3. Message header (default size): a long address broke inside the word ("bank.alerts@example.co / m"). Address and To are one line, shortened in the middle.
4. AX3 form rows broke labels inside words ("Na/me", "Fro/m", "Rec/eive/d") in Rule editor, Rule from message and Failed detail, and truncated them in Business detail / Routing problem ("Dom…", "Rout…", "Lumen Ho…"). From AX1 the caption now sits above its value.
5. AX3 mail list in select mode: the selection circle overlapped the sender. Fixed-size glyph.
6. (P2) OB04: the fixed domain now says "Routed just now".

### D3 findings: differences I did not fix (screen: what differs)
- OB06 Google handoff: board is its own screen ("Continue in your browser", accounts.google.com panel "Sign in and allow “Read, label and archive”", "Open browser"); app keeps OB05's lists with a small paragraph added and a disabled "Waiting for Google…" button.
- OB08 Filters: board lists all 5 filters in one list ("Label orders"…, the Delete one unchecked, footnote "“Delete” filters can't be imported. Inbox never deletes mail.", "5 filters found", "Import 3 as rules"); app shows "4 FILTERS FOUND" and a separate "CAN'T IMPORT" card, rows named by rule.
- OB11 Alfather not found: board has Skip, caption "Inbox works fully without it…", an "Alfather / Not reachable / Check" row, a 3-item checklist, "Try again" / "Set up later"; app has the D2 pairing caption, "You can pair later in Settings → Alfather.", "Finish" / "Look again".
- OB04: board leaves casprine.dev off (selection state); app turns every selectable domain on.
- NA01 Mail root: board puts All businesses above Needs attention; app puts Needs attention first. Board copy "Mail no rule matched" / "Couldn't be processed"; app "No rule matched" / "Couldn't be saved".
- NA04 Failed list: board groups by business ("Acme Studio · 1"), subtitle "1 message couldn't be processed", row preview is the reason ("Message is larger than 25 MB…"); app is a flat list, "couldn't be stored", preview "Couldn't store.".
- NA05: board reason text "31 MB is over the 25 MB limit. It was still delivered to Gmail."; app's reason text is generic (no sizes in the mock).
- NA06 Retrying: board is the detail screen ("Retrying…", "Fetching the message from Gmail instead", rows dimmed); app shows only the list row "Retrying…", the detail has no retrying state.
- NA07 empty: board "Nothing failed / Every message has been stored and sorted." with no subtitle; app "Every message was saved." plus a "0 messages couldn't be stored" subtitle.
- ML05/ML06: board swipe label "Unread" and toast "Archived · also in Gmail"; app "Read" (on an unread row) and "Archived".
- RU02 Rules edit: board has "New" at top left and the subtitle "Order is priority: the first rule that matches wins. Drag to reorder."; app has "New rule" / "Import from Gmail filters" rows, the footer sentence instead, a 36 pt inset title and 94 pt rows.
- RU06 label sheet title: board "Gmail label"; app "Label in Gmail". RU07: board error "Missing a closing bracket “)”."; app "Pattern isn't valid: missing closing )." and the same sentence a second time under "Test against recent mail". RU09: board "No matches / None of the last 200 messages match. Check the address or widen the subject."; app "Nothing would match…".
- MS07 Details: board "Received → Delivered Oct 11, 9:41 AM"; app has one "Received" row.
- AF02 Alfather connected: board has a "Read new mail" switch and "alfather.local · last seen just now"; app shows the text "On" and "Connected to your Inbox server · last seen just now". AF03 (reconnecting) has no board; app shows the title "Not reachable".
- GM05 alert copy: board "Labels and archive stop. Mail keeps arriving in Gmail and in Inbox."; app "Inbox stops labelling and archiving in Gmail. Mail still arrives…". All system alerts (GM05, RU10, RU02 delete) draw "Cancel" in system blue: the window has no tint.
- ST04b delete sheet: disabled "Delete everything" label (0.4 on the dark red) is almost invisible in dark, same issue as H-6.
- AX3 Mail root rows: counts and chevrons wrap under the titles, the All businesses glyph does not scale. AX3 search: the field shrinks to "inv…" because Cancel scales. AX3 Details: a two-line value ("98 KB · 1 / attachment") is centred. AX3 Gmail: the account e-mail is cut in the middle ("sam@…ail.com") though there is room. AX3 Health: rows have no vertical padding. AX3 message: the sender's HTML stays at its own small size while the header and subject scale.
- BZ03/BZ04: back label and centred title are 6 pt apart for "Businesses" + "Lumen Hosting"; longer names will collide.
- Pull-to-refresh spinner is the system grey, not the accent.
- ML08 empty page keeps the address chips ("All 7") from the business record while the list says "No mail yet".
- Harness only: BZ01 shows "Couldn't refresh" because the mock server has no GET /v1/businesses.

## D4 done (2026-10-31): Gate 12 fix list. All baselines are in `ios/InboxTests/Snapshots` (light and dark unless noted)
| # | Item | Status | Proof snapshot |
|---|---|---|---|
| 1 | F22 delete sheet disabled button (DD-1) | done | `ST04b-delete-sheet-{light,dark}` (field fill, secondary label, readable); new `ST04b-delete-sheet-enabled-{light,dark}` (red fill) |
| 2 | F28 sender HTML scales (DD-18.6) | done | `AX3-MS01-message-{light,dark}`: body text same order as the subject, no horizontal overflow; `MS01-message` unchanged (100 % at Large) |
| 3 | F21 app tint (DD-2) | done | `GM05-disconnect-alert-*`, `RU10-rule-delete-alert-*`: Cancel in app red |
| 4 | Copy batch (DD-8/9/13/14) | done | `NA01-needs-attention-root`, `NA04-failed`, `NA07-failed-empty`, `RU06-label-picker`, `RU07-rule-editor-invalid` (error once; Test button disabled), `RU09-rule-test-none`, `GM05-disconnect-alert` |
| 5 | F12 archive toast (DD-11) | done | `ML06-archived-toast` ("Archived · also in Gmail"); unit test covers the disconnected case ("Archived") |
| 6 | F5 Mail root order (DD-8) | done | `NA01-needs-attention-root`, `AX3-NA01-*` |
| 7 | F19 AF03 reconnecting (DD-16) | done | `AF03-alfather-reconnecting-*`. Cause was `state()` using the real clock instead of `AppClock`; the app now passes `AppClock.now`. The reconnecting window is now 5 to 30 min (was 15). |
| 8 | F32 BZ01 harness | done | `BZ01-businesses-settings-*` (mock `GET /v1/businesses` added) |
| 9 | AX3 batch (DD-18.1-18.5) | done | `AX3-ML01-businesses`, `AX3-SR02-search-results`, `AX3-MS07-details`, `AX3-GM04-gmail-catching-up`, `AX3-ST06-health-issues` |
| 10 | F29 Back label (DD-19) | done | `BZ04-business-detail-routing` ("‹ Back"), new `BZ04b-business-detail-long-name` (24 characters) |
| 11 | F7 NA04 grouping (DD-9) | done | `NA04-failed-light` |
| 12 | F13 RU02 layout (DD-12) | done | `RU02-rules-edit-light` |
| 13 | F3 OB11 structure (DD-7) | done | `OB11-alfather-not-found-{light,dark}` (AX3 baseline not added) |
Skipped as marked: NA06 retrying detail (DD-10), ML08 chips case, ML10 banner icon, GM06 radio glyph at AX3, pull-to-refresh tint, OB12 tracking.
Notes: (a) money amounts in the sample invoice break mid-number at AX3 ("$1,240.0 / 0") because the HTML wrapper has `overflow-wrap:anywhere`; that is the sender's table in a narrow column, not changed. (b) The OB11 checklist row "This phone is on the same Wi-Fi network" wraps as "Wi- / Fi" at 390 pt. (c) The Failed list shows its group header from the message's business, so the mock row (sender Lumen Hosting) sits under "ACME STUDIO · 1".
Re-recorded names: every baseline named above plus the knock-on Mail-root/Settings ones: `ML01-businesses`, `ML01-signed-out`, `GM02-*`, `OB13-*`, `ST01-settings`, `ST05-health-*`, `BZ06-routing-problem`, `NA06-*`, `OB10-alfather`, `GM03/GM06/GM07 AX3`, `AX3-GM01`, `AX3-ST01`, `AX3-ML01-signed-out`, `AX3-OB13`.
