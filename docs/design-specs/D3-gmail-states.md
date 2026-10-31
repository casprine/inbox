# D3 — Gmail states not on any board (design agent)

Tokens: `tokens.md`. Pattern notes: Banner = `.bnr` (card fill, r12, margin 8 16 6, pad 12 14, 15 pt, 18 pt icon) with the status colour on the **icon**, never a dot; dots (`stt`, 8 pt) are for status *rows*.

## Gmail rate-limited (write-back paused by Google, resumes by itself)
- Where: Gmail settings screen (connected state), directly under the account card. Also summarised in Health (ST06) as "Gmail · Paused, resuming".
- Banner: clock-pause icon in `warning` (`#B45309` / dark `#E0A03A`), bold lead **"Gmail is rate-limiting Inbox."** + **"Labels and archive are paused for a few minutes and will resume on their own."** No action.
- Account subline: `● Connected · Paused` (warning dot). "Waiting" row shows the live count.
- Not shown on the Mail root (mail keeps flowing; not worth a banner). Distinct from GM09 (import paused), which keeps its own board.

## Gmail not connected (never connected, or after Disconnect)
- Nav: back "Settings", centred title **Gmail**.
- Content (same pattern as OB05): `h2` 28 PolySans **"Connect Gmail"**; `cap` 15/1.45 secondary **"Inbox never sends mail. It needs Gmail only to keep it tidy."**; two short lists "Inbox will" (Label mail by business and rule · Archive mail your rules archive · Import older mail once) and "Inbox won't" (Send, delete or forward anything · Share your mail with anyone), as OB05.
- Primary button **Continue with Google** pinned at `bottom 106` (above the tab bar), 52 pt, `accentFill`. No Disconnect here.

## Signed-out account row (GM03 clarification)
Account card row: leading envelope icon (accent, 22 pt), name 16.5, subline 14 secondary with `● Disconnected` (warning dot). Reconnect button pinned at `bottom 106`. A **Disconnect Gmail** destructive row is allowed on this screen as a separate inset card below the account card (text `#D92D20`, centred, 56 pt), since the board omits it but the flow needs an exit.

## Delete-everything confirmation (ST04, not on a board)
System-style alert (`.alert` pattern: card r14, title 17/500, message 14 secondary, two actions split by hairline). Title **"Delete everything Inbox stored?"**; message **"This removes stored copies from your Inbox server. Your Gmail isn't touched, and new mail will keep arriving."**; actions **Cancel** (TA/500) | **Delete** (`#D92D20`/500). Success toast: "Stored copies deleted."

## Import running / done (GM07/GM08 as built, accepted)
Running: title + "x of y · n%", 6 pt bar, per-business rows, Pause (secondary ring) + Cancel (destructive text row, not accent text). Done: banner with check icon (ok green) + summary, per-business rows, "Import another range". Paused banner lead ends with a period.

## Update: delete-everything as built (accepted)
Bottom sheet "Delete everything?" with the explanation, a **typed confirmation code** field and a destructive "Delete everything" button (disabled at 0.4 opacity until the code matches), Cancel (TA) top right. This supersedes the alert above. Field placeholder: "Type the code".
