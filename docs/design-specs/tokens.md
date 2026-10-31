# Token spec v1.2 (design agent; v1.2 = D-011 fonts, D-013 red text) — supersedes plan §8.5 where different

Base tokens are unchanged (see `Palette.swift`, matches Foundations). **Added/changed:**

| Token | Light | Dark | Use | Contrast |
|---|---|---|---|---|
| `accent` | `#F83D34` | `#FF5B45` | dots, bars, icons, switch-on, tab bar, focus ring (non-text) | non-text ≥3:1 |
| `accentText` (TA) | `#BD251D` | `#FF5B45` | **all accent-coloured text**: back label, Cancel, Clear, Fix, Undo, Load images, links, selected tab label | 5.03 on ground, 4.55 on grouped / 6.08 dark |
| `accentFill` (AF) | `#C8281F` | `#FF5B45` | primary button, selected chip, badges with text | `onAccent` on it 4.58 / 6.08 |
| `onAccent` | `#F1E9D2` | `#1A0F16` | text on `accentFill` | |
| `tertiary` | `#A68C97` | `#7E6B76` | **non-text only**: chevrons, decorative icons, disabled | |
| `textTertiary` | `#7E576C` | `#907D87` | placeholder text only; timestamps, section headers, counts use `secondary` | 4.5+ |
| `hairlineStrong` | `#CDBDA5` | `#4A3342` | chip ring, switch off fill, sheet grabber, chevrons | |
| `field` | `#E5D9BC` | `#2C1B26` | search field, segmented control track | |
| `highlight` | `#FCE2DA` | `#4A2220` | search match background | |
| `knob` | `#FFFDF7` | `#F3EBD8` | switch knob | |
| `destructive` | `#D92D20` | `#F0584A` | destructive button **fill** only (white / `#1A0F16` text). **Not for text on cream** (3.99:1 on ground, 4.29 on card); red text uses `accentText` (D-013) | white on it 5.0 |
| `warning` | `#B45309` | `#E0A03A` | warning dot/text | |

Rules: G1–G3 in `DESIGN_QA.md`.
Typography (pt) — **Open Runde (D-011); full map, sizes and risks in `D4-signin-and-fonts.md` Part 2.** Headings Bold/Semibold, body Regular/Medium, mono = system monospaced. Summary: onboarding h1 32/36 Bold −0.8 · title 30/34 Bold −0.025em · h2 27/31 Bold −0.5 · subject 20/25 Semibold · sheet title 20 Semibold · row sender 15.5 (Regular read / Medium unread) · subject 14.5 · preview 14.5 secondary · time 13.5 secondary (tabular digits) · section header 12/Medium caps tracking .05em · button 17 Medium · body message 16/1.5 · caption 14.5/1.45 · footnote 13.
Fallback if a face is missing: system font, heading `.bold`/`.semibold`, body `.regular`/`.medium`.

## D-013 — which red where

Brand red `#F83D34` fails as text on cream (3.04:1). Two reds, never mixed up:

| Token | Value (light / dark) | Use |
|---|---|---|
| `accent` `#F83D34` / `#FF5B45` | fills without text, icons, large elements, non-text UI | unread dot, status dots, progress bars and rings, switch-on fill, tab bar selected **icon**, focus ring and error ring, chevron-less icon buttons (Archive, Open in Gmail, More), search highlight edge, banner/status icons that are red, checkmark circle in Appearance, app icon |
| `accentText` `#BD251D` / `#FF5B45` | **every piece of red text** on cream or card (5.03 ground, 5.41 card, 4.35 field) | back label, Cancel, Clear, Done, Fix, Undo, Try again, Retry (text), Sign in (banner action), links, Load images, selected tab **label**, form error text (sign-in inline errors), "Disconnect Gmail" and "Delete everything" **text rows**, Cancel import row, selected segmented label |
| `accentFill` `#C8281F` / `#FF5B45` | red fills that carry text | primary button (Get started, Sign in, Continue, Retry), selected chip, count badges. Light uses `#C8281F` so cream text reaches 4.58:1; `#F83D34` under cream text is only 3.04:1 and is **not** used for these |
| `destructive` `#D92D20` / `#F0584A` | red fills that carry white text | destructive button fill ("Delete everything" when enabled). Not text |

Open owner point: D-013 says `#F83D34` for fills. This spec keeps `#F83D34` for every fill without text and uses `#C8281F` only where text sits on the fill. If the owner wants one red for all fills, buttons must switch to a dark `onAccent` text (`#1A0F16`, 5.08:1 on `#F83D34`).

Radii: inset group/banner/field-group 12 · field 10 · chip 16 · button 14 · sheet 18 · switch 8/6.
