# Gate 12: design audit after D1 to D3 (design worker, 2026-10-31)

Method: every baseline in `ios/InboxTests/Snapshots` (light, about 70 screens; dark for about 40; AX3 light for 36 and dark for 7) was viewed as contact sheets and compared with the board copy (`extract.py`), D2 to D5, `tokens.md` v1.2 and the new `docs/design-specs/D6-gate12-decisions.md`. A spot read of `ios/InboxKit/Sources` (read only) confirmed red-text, size and tracking claims. No build or simulator run. Colour and spacing judgements come from downscaled sheets, so sub-2 pt differences are not claimed.

Severity rubric (written so the counts mean something): **P0** broken or wrong. **P1** a written spec is violated in a way a user can see AND it hurts legibility or access, or core content cannot be read. **P2** board or copy mismatch, order, polish, harness. Board-only differences are never P1 (spec silent = board wins, D-017, but it is not a defect class that blocks).

## 1. Result

**P0 0, P1 2, P2 24 (20 are in the fix list, 4 are left out as polish).** All H-1..H-17 from Gate 11 are closed. The 6 items Swift fixed in D3 are closed. Of the 32 D3 findings: 8 are closed by written spec (D6), 2 P1 and 20 P2 are open and in the fix list, 2 P2 are left out. Gate 12 does **not** pass: two P1 remain, one more Swift round (D4 in the coordinator's plan) is needed. Fix list: `docs/handoff/design-to-swift.md`.

## 2. Gate 11 H-items

| H | Item | Evidence | Verdict |
|---|---|---|---|
| H-1 | Red text tokens | GM03, GM01, ST04, GM07, AF02, BZ03, NA05 rows draw #BD251D like the back label; `grep` finds `p.accent` only on icons/glyphs and `destructive` nowhere as text colour; RU07 errors TA; Banner error icon accent | closed |
| H-2 | D4 §2.3 size table | MailRow 15.5/13.5/14.5/14.5 in code; shots show no clipping | closed |
| H-3 | Heading Bold/tracking | all titles Bold; onboarding 27/-0.5, h1 32/-0.8, sign-in 27 | closed |
| H-4 | Tabular digits | `monospacedDigit` in 13 places incl. row time, countdown | closed |
| H-5 | AX3 large title | AX3-ML01, RU01, ST01 light+dark: no mid-word break, Edit on its own row | closed |
| H-6 | Locked button | OB01b-locked light and dark: `field` fill, readable label | closed (D5 §2 adopted, see DD-1) |
| H-7 | OB07/OB12 | built, light/dark/AX3 | closed |
| H-8 | Snapshot coverage | every GATE11 §6 screen now has light+dark | closed (manual list in section 6) |
| H-9 | Pushed nav title colour | GM00, GM03, ST03, ST04, RU04 titles plum / cream | closed |
| H-10 | Small type | section headers, back/cancel look right; not measurable at sheet scale | closed (not pixel-verified) |
| H-11 | Stray divider | OB10 clean | closed |
| H-12 | Harness clip, locale | OB02/05/09/10/GM00/GM03 footers visible, OB09 "1,100" | closed |
| H-13 | Business page AX3 "·" | AX3-ML02-fixed: "·" ends line 1 | closed |
| H-14 | Subject 20, sheet 20, h1 tracking | code 20/-0.2, h1 -0.8 | closed |
| H-15 | BZ02 handle | custom 6-dot handle in card; drag is source/target only (accepted) | closed |
| H-16 | Secure field bullets | OB01b-locked, ready show dots | closed |
| H-17 | AX3 BZ02/Settings | BZ02 wraps between words, domain middle-truncates; ST01 no mid-word breaks | closed |

## 3. D3: items fixed by Swift (6)

| # | Item | Verdict |
|---|---|---|
| 1 | RU02 handles and delete controls | closed (RU02: red minus + handle visible, light and dark) |
| 2 | Rule delete confirms | closed (RU10 alert; Cancel colour is F21) |
| 3 | Header address broke mid-word | closed (MS01 and AX3-MS01 middle truncation) |
| 4 | AX3 form rows broke labels | closed (AX3-RU05, BZ04, NA05: caption above value, no mid-word) |
| 5 | AX3 select circle overlap | closed (AX3-ML07) |
| 6 | OB04 "Routed just now" | closed |

## 4. D3 findings graded

| # | Finding | Verdict | Decision / fix |
|---|---|---|---|
| F1 | OB06 own screen | closed, spec | DD-4: state of OB05, board's browser panel not built |
| F2 | OB08 one list vs split | closed, spec | DD-5 keep split |
| F3 | OB11 structure and copy | **P2 open** | DD-7 |
| F4 | OB04 casprine.dev off | closed, spec | DD-6 |
| F5 | NA01 section order | **P2 open** | DD-8 |
| F6 | NA01 copy | **P2 open** | DD-8 |
| F7 | NA04 grouping, subtitle, preview | **P2 open** | DD-9 |
| F8 | NA05 31 MB text | closed, spec | D2 §3 void board text; app matches D2 |
| F9 | NA06 retrying detail | **P2, left out** | DD-10 written, building optional |
| F10 | NA07 empty copy | **P2 open** | DD-9 |
| F11 | ML05 "Read"/"Unread" | closed, spec | DD-11 label names the action |
| F12 | ML06 toast | **P2 open** | DD-11 |
| F13 | RU02 layout | **P2 open** | DD-12 |
| F14 | RU06 title | **P2 open** | DD-13 |
| F15 | RU07 error copy and duplicate | **P2 open** | DD-13 |
| F16 | RU09 copy | **P2 open** | DD-13 |
| F17 | MS07 Received/Delivered | closed, spec | DD-15 one row unless data |
| F18 | AF02 switch | closed, spec | DD-16 no switch |
| F19 | AF03 not a real baseline | **P2 open** | DD-16: shot shows Not reachable, code has the state; harness must show Reconnecting |
| F20 | GM05 alert copy | **P2 open** | DD-14 |
| F21 | System alerts blue Cancel | **P2 open** | DD-2 (D3 already said TA; one-line tint) |
| F22 | ST04b disabled label invisible | **P1 open** | DD-1 (dark label is invisible, light barely) |
| F23 | AX3 Mail root counts/glyph | **P2 open** (counts accepted, glyph scale) | DD-18.1 |
| F24 | AX3 search field "inv..." | **P2 open** | DD-18.2 |
| F25 | AX3 Details centred value | **P2 open** | DD-18.3 |
| F26 | AX3 Gmail e-mail cut | **P2 open** | DD-18.4 |
| F27 | AX3 Health rows touch | **P2 open** | DD-18.5 |
| F28 | AX3 message HTML stays small | **P1 open** | DD-18.6 (primary reading surface does not follow Dynamic Type) |
| F29 | BZ03/BZ04 back label + title 6 pt | **P2 open** | DD-19 |
| F30 | Pull-to-refresh grey | closed, spec | DD-17 |
| F31 | ML08 chips + "No mail yet" | **P2, left out** | DD-20: harness inconsistency, not a production state |
| F32 | BZ01 "Couldn't refresh" (harness) | **P2 open** | harness only: mock `GET /v1/businesses`, so the baseline shows the normal list |

Also left out (P2, polish): ML10 banner uses a dot, spec says icon (DD-21); OB12 tracking -0.8 vs board -0.68 (invisible); GM06 radio glyphs do not scale at AX3.

Counts: P1 = F22, F28. P2 open and in fix list = 20 (F3, F5, F6, F7, F10, F12, F13, F14, F15, F16, F19, F20, F21, F23, F24, F25, F26, F27, F29, F32). P2 left out = 4 (F9, F31, ML10 dot, GM06 radio glyph). Closed by spec = 8 (F1, F2, F4, F8, F11, F17, F18, F30). 8 + 2 + 20 + 2 = 32 findings; P2 total 24 = 20 + F9 + F31 + ML10 dot + GM06 radio.

## 5. Per-screen status (light, dark, AX3 as available)

Legend: ok = matches board/spec; P1/P2 = open item (see section 4); sp = difference closed by written spec.

| Screen | L | D | AX3 | Status | Notes |
|---|---|---|---|---|---|
| OB01 Welcome | y | y | y | ok | |
| OB01b sign-in, all states | y | y | y | ok | locked now `field`, readable |
| OB02 Domains | y | y | | ok | |
| OB03 Route domain sheet | y | y | | ok | |
| OB04 Domains fixed | y | y | | ok, sp | DD-6 |
| OB05 Connect Gmail | y | y | | ok | |
| OB06 Handoff | y | y | | ok, sp | DD-4 |
| OB07 Gmail connected | y | y | y | ok, sp | DD-3 |
| OB08 Filters | y | y | | ok, sp | DD-5 |
| OB09 Backfill | y | y | | ok | |
| OB10 Alfather found | y | y | | ok | |
| OB11 Alfather not found | y | y | | P2 F3 | |
| OB12 Done | y | y | y | ok | type row added |
| OB13 Import banner | y | y | y | ok | |
| ML01 Businesses | y | y | y | P2 F5, F6, F23 | NA01 order/copy |
| ML01 signed out | y | y | y | ok | |
| ML02 Business page | y | y | y | ok | |
| ML05 Swipe | | | | manual | seen live; DD-11 |
| ML06 Toast | y | y | | P2 F12 | |
| ML07 Select | y | y | y | ok | |
| ML08 Empty business | y | y | | P2 left out F31 | |
| ML09 All businesses | y | y | y | ok | |
| ML10 Refresh failed | y | y | | ok (dot, P2 left out) | |
| MS01 Message | y | y | y | P1 F28 (AX3) | |
| MS03 Thread | y | y | y | P1 F28 (AX3 HTML shares the issue) | |
| MS04 Images blocked | y | y | | ok | |
| MS05 Attachment | y | y | | ok | preview is system, manual |
| MS06 More sheet | y | y | | ok | |
| MS07 Details | y | y | y | P2 F25, sp F17 | |
| MS08 Original | y | y | | ok | |
| MS09, MS09b Not found | y | y | | ok | dot per board |
| SR01 to SR04 | y | y | y (SR02) | P2 F24 | |
| NA01 root | y | y | y | see ML01 | |
| NA02 Unrouted | y | y | y | ok | |
| NA03 Rule from message | y | y | | ok | |
| NA04 Failed list | y | y | | P2 F7 | |
| NA05 Failed detail | y | y | y | ok, sp F8 | |
| NA06 Retrying | y | y | | P2 left out F9 | list row only |
| NA07 Empty | y | y | | P2 F10 | |
| RU01 Rules | y | y | y | ok | |
| RU02 Edit | y | y | | P2 F13 | |
| RU04 Import filters | y | y | | ok | |
| RU05 Editor | y | y | y | ok | |
| RU06 Label picker | y | y | | P2 F14 | |
| RU07 Invalid | y | y | | P2 F15 | |
| RU08, RU09 Test | y | y | y (RU08) | P2 F16 | |
| RU10 Delete alert | y | y | | P2 F21 | |
| GM00 Not connected | y | y | | ok | |
| GM01 Connected | y | y | | ok | |
| GM02 Mail banner | y | y | | ok | |
| GM03 Signed out | y | y | y | ok | |
| GM04, GM04b Catching up, rate-limited | y | y | y | P2 F26 | |
| GM05 Disconnect alert | y | y | | P2 F20, F21 | |
| GM06 to GM07 Import | y | y | y | ok | |
| ST01 Settings | y | y | y | ok | |
| ST03 Appearance | y | y | | ok | |
| ST04 Privacy | y | y | | ok | |
| ST04b Delete sheet | y | y | | P1 F22 | |
| ST05 About, licence | y | y | y | ok | |
| ST06 Health | y | y | y | P2 F27 | |
| AF01, AF02, AF04 | y | y | y (AF04) | ok, sp F18 | |
| AF03 Reconnecting | y | y | | P2 F19 | shot shows Not reachable |
| BZ01 Businesses in Settings | y | y | | P2 F32 | shows error banner |
| BZ02 Edit | y | y | y | ok | |
| BZ03, BZ04 Business detail | y | y | y | P2 F29 | |
| BZ05 Add business | y | y | | ok | |
| BZ06 Routing problem | y | y | y | ok | |

## 6. Regression sweep (font and size changes)

No truncation, clipping or wrong red found at default size in any viewed screen. Checked specifically: row sender/preview/time columns (ML02, ML09, SR02: time column holds "Yesterday" and "16:10" on one line), chips at 13.5 (no clipping, horizontal scroll at AX3), section headers 12/+0.6 (two lines allowed at AX3), banners at 14.5 (GM02/GM03/OB01b no overflow), message header and subject (MS01/MS03), disabled switches and the segmented control (SR03), red counts on Mail root (TA), Delete rule fill (destructive with white label, correct), dark mode reds (#FF5B45 everywhere, no #D92D20 text). Missing states: NA06 detail (F9), AF03 shot (F19). Dark AX3 was viewed for 7 screens only (ML01, RU01, ST06, OB01b-locked, MS01, GM04, OB12): same layout as light, no new issue; the other 29 AX3 screens were viewed in light only.

**Cannot be proved by a baseline; verify by hand** (device or simulator, light and dark):
1. ML05 swipe actions: swipe an unread row right/left; labels "Read" and "Archive", plum and accent fills, full-swipe archives and shows the toast. Check a read row shows "Unread".
2. Pull-to-refresh spinner on Mail, Rules, Search: appears, spins, the "Couldn't refresh" banner replaces it when offline.
3. MS05 QuickLook: tap an attachment; system sheet, Done is red, back to the message.
4. RU03 Rules empty: delete all rules (or use a fresh server): "No rules yet", New rule and Import buttons, no tab bar overlap.
5. Keyboard-up states: SR01 with keyboard, sign-in fields focused (ring, Next/Go keys), rule editor fields: Cancel/Save remain reachable, the field is not hidden.
6. VoiceOver: sign-in per D4 §1.8, Mail row ("sender, subject, time, unread"), toolbar icons labelled, banner actions, check circles announce state.
7. BZ02 drag to reorder (no live row displacement is accepted): drag one row, drop, order persists after relaunch.
8. OB06/OB07 live Google flow (sandbox has no Google config): handoff state, return to OB07, cancel in the browser returns to OB05 with a banner.
9. Dynamic Type AX3 dark for the 29 screens not viewed (swap the sheet to dark).
10. After DD-18.6: open a wide newsletter at AX3 and confirm no horizontal scroll and no overlap.

## 7. Design decisions (for owner review; full text in `docs/design-specs/D6-gate12-decisions.md`)

| DD | Decision | Reason |
|---|---|---|
| 1 | Meaningful disabled buttons (lock-out, delete-until-code): `field` fill, `secondary` label; plain empty-form disabled stays .4 | D5 §2 adopted by C-24; same invisibility bug on the delete sheet |
| 2 | App window tint = `accentText`; no system blue in alerts; destructive role stays system red | D3 already specified Cancel in TA |
| 3 | OB07 "Step 3 of 7" is correct | board prints 3 on OB05/06/07 |
| 4 | OB06 is a state of OB05 (waiting button), no separate screen | system browser sheet replaces the board's mock panel |
| 5 | OB08 keeps importable list + "Can't import" card | unchecked untickable row reads as a bug |
| 6 | OB04: all Ready/Fixed domains default on | board's off row is sample data |
| 7 | OB11 follows the board (Skip, caption, row, checklist, Try again / Set up later) minus the MCP-bridge item, plus the D2 pairing hint | D2 said "keep"; MCP bridge step was removed by D2 |
| 8 | NA01: All businesses above Needs attention; copy "Mail no rule matched" / "Couldn't be processed" | spec silent, board wins; "processed" covers store and parse failures |
| 9 | NA04 grouped by business, preview = failure title, "processed" wording; NA07 board copy | consistency with NA02; 31 MB text void (D2 §3) |
| 10 | NA06 retrying detail defined, building optional | undesigned in app, seconds-long state |
| 11 | Swipe label names the action; toast "Archived · also in Gmail" only when write-back is connected | Mail.app convention; no false claims |
| 12 | RU02: subtitle under title, 56 pt rows, New rule + Import rows kept | board has no import entry point |
| 13 | RU06 "Gmail label"; RU07 board error once; RU09 board "No matches" | spec silent, board wins |
| 14 | GM05 alert copy follows the board | spec silent |
| 15 | MS07 single Received row unless the server has a Delivered time | do not invent data |
| 16 | AF02/AF04 show On/Paused text, no switch; AF03 Reconnecting = 5 to 30 min without contact | no server pause control; Health threshold is 5 min |
| 17 | Pull-to-refresh stays system grey | no reliable tint API |
| 18 | AX rules: glyphs scale, Cancel capped, values leading-aligned, e-mail not mid-truncated, rows padded, sender HTML scales via text-size-adjust | D3 AX3 findings |
| 19 | Back label collapses to "Back" when closer than 12 pt to the title | BZ03/BZ04 collisions |
| 20 | Chips only when an address has visible mail; all-archived shows "Nothing in Mail" | ML08 inconsistency |
| 21 | Banners use icons, dots only in status rows (MS09 board keeps its dot) | D3 pattern, board exception |
| T | Type rows added: OB12 title Bold 34/37 -0.8, check 24 pt accent, body 16.5/1.45 | same -2 pt step as OB01; spec gap noted by Swift |
| R | Severity rubric in section 1 | so P1 means harm, not difference |

## 8. Could not verify

Rendered boards (only `extract.py` text and CSS, no picture of any board); 1 to 2 pt spacing and exact colour values from downscaled sheets; the 29 dark AX3 screens; everything in the manual list above; whether Swift's `RedTextTests` still passes (not run).

## 9. Gate 12 close-out (after round D4, design worker, 2026-10-31)

Viewed: `ST04b-delete-sheet` light/dark/enabled light/dark, `AX3-MS01-message` light/dark, `AX3-OB11` light/dark, `OB11`, `AF03`, `BZ04`, `BZ04b`, `NA01`, `NA04`, `NA07`, `RU02`, `RU07`, `RU09`, `GM05`, `ML06`, `AX3-ML01`, `AX3-SR02`, `AX3-MS07`, `AX3-GM04`, `AX3-ST06` (light, dark). Not re-viewed: `BZ01` (harness item 8, taken from Swift's report), `AX3-NA01`.

| Fix item | Verdict | Evidence |
|---|---|---|
| 1 F22 delete sheet (P1) | **closed** | disabled: `field` fill, readable `secondary` label in light and dark; enabled: red fill, white (light) / dark (dark) label |
| 2 F28 sender HTML (P1) | **closed** | AX3-MS01 light/dark: body text now same order as the subject, no horizontal overflow |
| 3 F21 tint | closed | GM05 Cancel in app red |
| 4 copy batch | closed | NA01, NA04, NA07, RU07 (error once, Test button disabled), RU09, GM05 all show D6 strings |
| 5 F12 toast | closed | "Archived · also in Gmail" |
| 6 F5 order | closed | All businesses above Needs attention |
| 7 F19 AF03 | closed | "Reconnecting..." shown |
| 8 F32 BZ01 | closed (per Swift, not re-viewed) | |
| 9 AX3 batch | closed | tray glyph scales, search field shows "invoice", Details value left-aligned, Gmail e-mail whole, Health rows spaced |
| 10 F29 back label | closed | "‹ Back" with "Lumen Hosting" and "Harbor & Pine Provisions" |
| 11 F7 NA04 grouping | closed | header "ACME STUDIO · 1" (the mock row's sender is Lumen Hosting under Acme: mock data, not a defect) |
| 12 F13 RU02 | closed | subtitle under title, title not inset, rows about 56 pt |
| 13 F3 OB11 | closed | structure as DD-7, AX3 baseline now exists and wraps cleanly |

Nothing reopened. **P0 0, P1 0.** Accepted as polish (P2, not fixed): NA06 retrying detail, ML08 chips case, ML10 banner dot, GM06 radio glyph at AX3, pull-to-refresh tint, OB12 tracking, OB11 checklist "Wi-Fi network" may wrap as "Wi- / Fi" at narrow widths (use a non-breaking hyphen if touched again), sample invoice amounts breaking mid-number at AX3 (sender's table, `overflow-wrap:anywhere`). **Gate 12 passes.** The manual list in section 6 still applies.
