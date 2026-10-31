# D5: proposals (NOT adopted; need owner OK before they become spec)

## 1. Tab bar labels
D4 §2.3 says tab labels stay system; tokens.md and the board draw 11/500. Proposal: keep Open Runde Medium 11 (what the app does today). Tab bar is custom (no system chrome).

## 2. Locked sign-in button (D4 §1.5 "Too many attempts")
Problem: the disabled label at .4 opacity on the red fill is about 1.5:1 in light and unreadable in dark. Proposal: when locked, fill = `field` (light #E5D9BC / dark #2C1B26), label = `secondary` (light #7A5268, 5+:1; dark #C2AFB8), same 52/r14. Other disabled states (empty) keep the .4 dim. Interim minimal fix: dim the fill only, keep the label at full `onAccent`.

## 3. Onboarding OB07 and OB12
Boards exist; the app has no matching screens. If the owner wants a shorter flow, void OB07/OB12 here. No new design proposed.
