# D6: Gate 12 spec decisions (design worker, 2026-10-31)

Written to close the D3 findings that were really spec gaps or board-vs-spec conflicts. Rule used throughout (D-017): the written spec wins over a board; where the spec is silent the board wins; where neither exists (state not drawn) this file defines it. Each item is numbered DD-n and logged with its reasoning in `docs/design-audit/GATE12.md` under "Design decisions" so the owner can overrule it. Tokens: `tokens.md`. Type table: `D4-signin-and-fonts.md` §2.3 (this file adds rows, it does not change existing ones).

## Type table additions (D4 §2.3 had no row)

| Style | Spec | Notes |
|---|---|---|
| Final-step title (OB12 "You're set up.") | **Bold 34/37, tracking -0.8** | Board draws 36 PolySans; same -2 pt step as OB01 (34 to 32). Larger than a step title (h2 27) on purpose: it is the end of onboarding. `.largeTitle`, wraps at AX. |
| Final-step check mark (OB12) | **accent stroke check, 24 pt box, 2 pt stroke**, 20 pt above the title | An icon, so `accent`, not `accentText`. |
| Final-step body | Regular 16.5/1.45 `secondary` | Same as OB01 intro. |

## DD-1 Disabled buttons that carry meaning (extends D5 §2, adopted by C-24)

A filled button that is disabled and whose label tells the user something (sign-in lock-out countdown "Try again in 4:32", "Delete everything" until the code matches) uses fill `field`, label `secondary` (light #7A5268 on #E5D9BC, about 4.6:1; dark #C2AFB8 on #2C1B26), same size and radius, still not hit-testable. A disabled button with an ordinary empty-form reason (Sign in with empty fields, Save on an unchanged form) keeps the 0.4 dim. The delete sheet button must not use the dimmed destructive fill with a 0.4 label (invisible in dark).

## DD-2 System alerts and confirmation dialogs use the app tint

The app window tint is `accentText` (light #BD251D, dark #FF5B45). Alert and action-sheet default actions ("Cancel") therefore draw in the app red, as D3 already specifies ("Cancel (TA/500)"). The destructive action keeps the system destructive role (system red). No system blue anywhere in the app. Applies to GM05, RU10, the RU02 delete alert, ST04b if it is ever a system alert, and any future alert.

## DD-3 OB07 "Step 3 of 7" (closes the Swift question)

Correct as built. The boards print "Step 3 of 7" on OB05, OB06 and OB07: Connect Gmail, the browser handoff and "Gmail connected" are one step (3) with three states. Do not renumber.

## DD-4 OB06 browser handoff

OB06 is a transient state of OB05, not a separate screen. While `ASWebAuthenticationSession` is open the app keeps OB05, shows the paragraph "Continue in your browser" with "Google asks you to approve access there. You'll come straight back here.", and the pinned button reads "Waiting for Google..." with a 17 pt spinner and is disabled (use the DD-1 style). The board's `accounts.google.com` panel and "Open browser" button illustrate the system browser sheet; they are not built. "Skip for now" stays.

## DD-5 OB08 filters

Keep the app's split: "N filters found" lists only importable filters (each with a check, all on by default), and a second card "Can't import" lists the ones Inbox refuses (delete filters) with the line "Inbox never deletes mail." The board's single list with an unchecked Delete row says the same thing in one card; the split is clearer because an unchecked, un-tickable row looks like a bug. Count in the title and the button ("Import 4 as rules") follow the importable ones.

## DD-6 OB04 domain defaults

Every domain whose state is Ready or Fixed is on by default; the user turns off the ones that are not businesses. The board's off casprine.dev is sample data, not a rule.

## DD-7 OB11 Alfather not found (D2 §4 said "keep the board")

Structure, top to bottom: nav Back left, **Skip** right; step line "Step 6 of 7"; title "Alfather not found"; caption **"Inbox works fully without it. Mail just won't be read in the background yet."**; one inset row **Alfather** with status text "Not reachable" (no Check action; Try again does it); short checklist card "Alfather is running on your Mac" / "This phone is on the same Wi-Fi network" (the board's third item, "The MCP bridge is enabled", is dropped because D2 removed the MCP-bridge step: Alfather connects out); hint line **"You can pair later in Settings → Alfather."**; pinned **Try again** (primary) and **Set up later** (plain, same as Skip). Replaces "Finish / Look again" and the OB10-style caption.

## DD-8 Mail root (NA01)

Order: search field, banners, **All businesses** row, **Needs attention** group (only when Unrouted or Failed is non-zero), then the business list with its "N businesses" header. Copy: Unrouted subtitle **"Mail no rule matched"**; Failed subtitle **"Couldn't be processed"**. "Processed" covers both store and parse failures (D2 §3 has both variants); the detail screen keeps the specific title ("Couldn't store." / "Couldn't read this message."). Count colour: `accentText`.

## DD-9 Failed list (NA04) and empty (NA07)

- Title "Failed", subtitle **"N message(s) couldn't be processed"**.
- Grouped by business exactly like Unrouted (NA02): caps header "ACME STUDIO · 1". Row: sender, date, subject, then the failure title as the preview line ("Couldn't store." / "Couldn't read this message."). The board's "Message is larger than 25 MB..." is void (D2 §3: 25 MiB cannot reach us).
- Footnote: **"These were delivered to Gmail, but Inbox couldn't process them."**
- Empty (NA07): title **"Nothing failed"**, message **"Every message has been stored and sorted."**, no subtitle line under the page title.

## DD-10 NA05/NA06 retrying detail (new state, not required for Gate 12)

Opened from a row that is retrying: the status banner is replaced by a 17 pt spinner + **"Retrying..."** (Medium) and **"Fetching the message from Gmail instead"** (secondary); the Message and What happened cards stay but at opacity .5; the action rows are hidden. Ends on its own: success pops back with the Archived-style toast "Saved", failure returns to the normal failed detail. Short-lived (seconds), so building it is optional; the list row "Retrying..." is enough until then.

## DD-11 Swipe and toast wording (ML05, ML06)

- Swipe label names the action: **"Read"** on an unread row, **"Unread"** on a read row (as Mail.app). The board shows the second case. Archive keeps "Archive".
- Toast after archive: **"Archived"**; when Gmail write-back is connected the toast is **"Archived · also in Gmail"**. With Gmail disconnected it stays "Archived" (the claim would be false).

## DD-12 Rules edit mode (RU02)

Large title "Rules" at the normal title position (no extra 36 pt inset), subtitle directly under it **"Order is priority: the first rule that matches wins. Drag to reorder."** (replaces the footer sentence). Nav: **Done** right. Under the subtitle one inset card with two rows, **New rule** and **Import from Gmail filters** (kept from the app: the board's "New" top-left button would leave the import without an entry point). Rule rows: 56 pt minimum, two-line rows grow only as needed (title 16, subtitle 13.5), red minus on the left, custom reorder handle on the right.

## DD-13 Rule editor and test copy (RU06, RU07, RU09)

- Label sheet title **"Gmail label"** (board).
- Invalid pattern error, once, under the field: **"Missing a closing bracket “)”."** Other regex errors keep the form "Pattern isn't valid: <reason>." Do not repeat the sentence under "Test against recent mail"; that button is simply disabled while the pattern is invalid.
- No matches: title **"No matches"**, message **"None of the last 200 messages match. Check the address or widen the subject."** The nav title is the rule name.

## DD-14 Gmail disconnect alert (GM05)

Title "Disconnect Gmail?", message **"Labels and archive stop. Mail keeps arriving in Gmail and in Inbox."**, actions Cancel (DD-2 tint) and Disconnect (destructive).

## DD-15 Message details (MS07)

Keep one **Received** row. Add **Delivered** (to Gmail) as a second row only when the server provides a separate delivered-to-Gmail time; the board's pair assumes two timestamps and we do not invent one. Size value left-aligned when it wraps (DD-18).

## DD-16 Alfather screens (AF02, AF03, AF04)

- No "Read new mail" switch. The server has no pause control for Alfather; unpairing is the off switch. The row stays as a value row: **On** (connected) or **Paused** (not connected). The board's switch is void.
- AF02 subtitle per D2 §4 ("Connected to your Inbox server · last seen just now").
- **AF03 Reconnecting** is a real state: paired, no contact for more than 5 minutes but less than 30 minutes (the Health threshold is 5 min). Title **"Reconnecting..."**, subtitle **"Alfather will pick up where it left off."** Beyond 30 minutes it becomes AF04 "Not reachable". The snapshot named AF03 must show this state.

## DD-17 Pull to refresh

System default spinner accepted. SwiftUI `refreshable` does not expose a reliable tint; the tint of the window (DD-2) is applied where the system allows it. Not a defect.

## DD-18 Accessibility sizes (AX1 and up)

1. Row trailing counts and chevrons may drop under the title, left-aligned, when they do not fit (Businesses root, Settings). Accepted. The leading glyphs (All businesses tray, Settings icons) scale with Dynamic Type (`@ScaledMetric`), minimum 22 pt.
2. Search field: the Cancel button is capped at `.xxxLarge` so the field keeps at least 60 % of the row width; the field shows at least 8 characters.
3. Multi-line values in value rows ("98 KB · 1 attachment") are leading-aligned under their caption, never centred.
4. Account e-mail rows (Gmail): no middle truncation while a line is free. At AX1+ the leading envelope icon is hidden and the address may scale down to 0.75 before middle truncation.
5. Health and other check rows: 12 pt vertical padding at every size so rows never touch.
6. **Sender HTML scales with Dynamic Type.** The web view applies `-webkit-text-size-adjust` as a percentage equal to the user's content-size factor (100 % at Large, up to 200 % at AX3). The sender's layout stays theirs; text is no longer fixed at 11 to 13 pt while the app header is 40 pt.

## DD-19 Pushed navigation bar collisions (BZ03, BZ04)

On a pushed screen with a centred title: if the back label and the title would be closer than 12 pt, the back label becomes **"Back"** (chevron + "Back"). Titles are never truncated before 2 lines. Business pages with long names ("Harbor & Pine Provisions") therefore show "Back".

## DD-20 Empty business page (ML08)

An address chip row is shown only when at least one address has visible mail. A business whose mail is all archived shows "Nothing in Mail" / "Everything here is archived." (not "No mail yet", which is for a business that has never received mail). The shot with chips plus "No mail yet" is a harness inconsistency, not a production state; do not build for it.

## DD-21 Banner markers (clarification of D3)

Status banners (Banner component) carry an 18 pt icon in the status colour; dots belong to status rows. MS09's board draws a dot inside the banner row on purpose (`stt`), so MS09/MS09b keep the dot. Other banners with a dot (ML10 "Couldn't refresh") should switch to the warning triangle when next touched; polish, not blocking.
