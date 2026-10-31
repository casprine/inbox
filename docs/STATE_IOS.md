# iOS state (Swift agent)

Last verified 2026-10-12 (clean build, dedicated simulator `Inbox iPhone 17 Pro`):
`make ios-test` 158 tests pass (unit, feature, contract against captured real server responses, 40+ light/dark snapshots incl. AX3 Dynamic Type),
`make ios-ui-test` 4 pass (mock data), `make ios-e2e` 8 pass (real dev server, opt-in `InboxLiveTests`), unsigned Release archive succeeds.

## Built
Mail (businesses, business page, all businesses, select/swipe/archive with undo, offline outbox), Message (thread, images blocked/allow, attachments, details, original, Gmail
not found), Search, Rules (list, editor, label picker, test, import filters), Attention (Unrouted, rule from message, Failed), Gmail (status, connect, reconnect, disconnect,
catching up, rate limited), Businesses Edit mode (reorder/hide), offline message cache, paginated Search, Accessibility (VoiceOver actions, Dynamic Type to AX3, Reduce Motion), Onboarding, Settings (Businesses, Health, Appearance, Privacy, About, Alfather), Import older mail, Privacy (retention, delete everything).

## Tests that need the dev server
Only `InboxLiveTests` (`make ios-e2e` starts and stops the server itself; without INBOX_API_BASE_URL they skip). Every other iOS test uses mock URL protocols, in-repo fixtures or the app's `-UseMockData` mode. `LiveContractTests` decode captured real responses
(`ios/InboxTests/Fixtures/live-samples.json`, refresh it from `make dev-server`). Live flows are only checked by hand against `make dev-server`
(`SIMCTL_CHILD_INBOX_API_BASE_URL=http://127.0.0.1:18090 SIMCTL_CHILD_INBOX_DEV_TOKEN=dev-token-inbox-local-0000 xcrun simctl launch booted com.casprine.inbox`).

## Only verifiable with real accounts or devices
- **Password sign-in over Tailscale**: verified against the dev server on 127.0.0.1 only. Not verified: a `.ts.net` https address, the phone reaching it through Tailscale, TLS failure wording against a real bad certificate, and the iCloud Keychain "Save Password?" prompt on a device (the password field has `textContentType(.password)`).
- **Google (Gmail)**: the OAuth browser leg (`ASWebAuthenticationSession`, `inbox://gmail/connected` deep link), consent screen, `invalid_grant` sign-out, label list, filters import, rate-limit pause and Gmail write-back results all need a real Google account and OAuth client. Tested only with a fake authenticator and mock responses.
- **Cloudflare**: domain list, routing check and "Fix routing" instructions need a real Cloudflare account and token; tested against mocks and the server's fakes.
- **Alfather**: Bonjour discovery (`_alfather._tcp`), the LAN handoff (`POST /pair` with `mcp_url`, token, scope; DECISIONS Q-T31-3) and "connected" status need a real Alfather instance. The handoff contract is assumed.
- **Real device**: Dynamic Type and accessibility passes, real fonts (PolySans, Lausanne and GT America Mono are not bundled, D-005), push/background behaviour, keychain on a signed build, local-network permission prompt for Bonjour.
- **Failed-item retry**: the demo data has no failed items; retry (including `needs_gmail` and `not_in_gmail`) is covered by mocks only.

## Known limits
- Snapshot baselines are recorded on iPhone 17 Pro / iOS 26.5 (`make ios-record-snapshots` after an intended UI change).
- After changing a type in one InboxKit module, an incremental build can crash at launch (stale module); delete `~/Library/Developer/Xcode/DerivedData/Inbox-*/Build` and rebuild.
- The app opts out of Liquid Glass (`UIDesignRequiresCompatibility`); if Apple removes that key the toolbar buttons get glass capsules again.
- Our tests use their own simulator (`make ios-sim`) because other projects on this Mac share "iPhone 17 Pro" and steal the foreground (blank snapshots).

## Signing, TestFlight and the server URL (owner steps; nothing here has been done)
1. **Apple Developer Program account** (paid) and an App ID for `com.casprine.inbox`. No special capability is needed (Sign in with Apple was removed, D-012). Local-network access is declared in Info.plist (Bonjour `_alfather._tcp`, usage string); no other capabilities.
2. In `ios/project.yml` set `DEVELOPMENT_TEAM` (currently empty) and remove `CODE_SIGNING_ALLOWED: "NO"` for release builds. There is no entitlements file. Run `make ios-gen`.
3. **Server URL**: the person types it on the sign-in screen (the Tailscale Serve host, e.g. `mini.tail1234.ts.net`) and the app remembers it (UserDefaults `api_base_url`). `InboxAPIBaseURL` in Info.plist is now only a fallback for the signed-out state; leave it empty. https only (ATS allows plain http to local networks). `INBOX_DEV_TOKEN` / `INBOX_API_BASE_URL` environment variables only work in Debug builds and the simulator.
4. On the server set `OWNER_PASSWORD` (see `docs/DEPLOY.md`); the phone needs Tailscale to reach the Serve address.
5. **Archive and upload**: `xcodebuild archive -project ios/Inbox.xcodeproj -scheme Inbox -configuration Release -destination 'generic/platform=iOS' -archivePath build/Inbox.xcarchive INBOX_API_BASE_URL=https://…`, then Xcode Organizer → Distribute App → App Store Connect (TestFlight). Create the app record in App Store Connect first (name, bundle id, privacy questionnaire: the app collects nothing itself; mail stays on the owner's server).
6. **Fonts**: Open Runde (SIL OFL, D-011) is bundled and registered in `UIAppFonts`; mono is the system monospaced font. Credit line in Settings → About; the full licence text is `Inbox/Fonts/OFL.txt` (in the bundle, not displayed in the app).
7. App icon and launch screen are still the defaults (design has not delivered an icon).
8. CI: `docs/handoff/swift-to-backend.md` has the `ios` job (tests + unsigned archive); it has never run on GitHub.

## Run on a device (2026-10-14)
Guide: `docs/RUN_ON_DEVICE.md`. Debug is now signed automatically with team `2QG5WSN24S`; Release stays unsigned. No app entitlements exist, so nothing blocks provisioning. Debug reads the server URL and dev token from git-ignored `ios/Local.xcconfig` (`make ios-local-config`; Info.plist keys `InboxDevAPIBaseURL`/`InboxDevToken`, read by `DevConfig`), env vars still win. `NSAllowsLocalNetworking`, `NSLocalNetworkUsageDescription` and Bonjour were already in the plist. `LAN=1 make dev-server-start` serves the LAN.
- Verified: `generic/platform=iOS` Debug build succeeds with `CODE_SIGNING_ALLOWED=NO`; built Info.plist carries the config keys; dev server with `LAN=1` answers `/healthz` and `/v1/businesses` (with token) on the Mac's LAN IP 10.15.1.182 (from the Mac itself, not from a phone).
- NOT verified: the signed build. Without `-allowProvisioningUpdates` xcodebuild stops at "No profiles for com.casprine.inbox"; I did not pass it because it registers the app ID in the owner's Apple developer account. Xcode does this on the first Run from the GUI. Also unverified: install, the local-network prompt and phone-to-server reachability (no phone access by design). The Mac firewall was not checked.
- Port 18090 on 127.0.0.1 is held by another project's `api` here; `make ios-e2e` would collide with it.
- After the project change: `make ios-test` and `make ios-ui-test` (4 tests) both pass from a clean build.

## Polish pass after first device run (2026-10-16)
Owner feedback: search pushes the layout up, scrolling is slow. Fixes: tab bar hidden on the Search route (the keyboard was lifting it and squashing the screen); per-row cost cut (font availability and scaled sizes cached, DateFormatters cached, sections/last-row computed once per update instead of per row); new scheme `Inbox Device` (config `Demo`: optimised, signed, reads Local.xcconfig) for the phone. The dev token/URL gates no longer depend on `#if DEBUG`; store builds stay clean because only Debug/Demo include Local.xcconfig. Not measured on a device.
- App icon: `ios/Inbox/Assets.xcassets/AppIcon.appiconset` (design's set), shots in ios/docs/appicon-home-*.png.

## Server sign-in replaces Sign in with Apple (2026-10-31, D-012)
- **Flow:** Welcome (OB01) only navigates. "Get started" and "I already set this up" open the sign-in screen (OB01b, `FeatureOnboarding/SignInScreen.swift`, model `SignInModel`) in mode `new` / `existing`; mode `signedOut` exists in the model and snapshots but nothing opens it yet (no session-lost detection or "You've been signed out" banner exists in the app). `POST /v1/auth/login {password, device_name}` goes to the typed address (`APIClient.login`, 15 s timeout); the device token goes to the Keychain, the normalised address to UserDefaults `api_base_url`, the password is dropped. The client switches its base URL after the first successful login.
- **Address rules** (`API/ServerAddress.swift`): https added when missing, host lower-cased, path/query/trailing slash dropped, port kept; http only for localhost, `*.local`, `*.localhost`, loopback and private IPv4 (127/8 added to the spec list so the dev server works).
- **States:** empty, ready, loading, invalid URL (+ public http), wrong password (401 `invalid_credentials`: password cleared, no "tries left" text because the server sends none), locked out (429 `Retry-After`: banner and button count down, kept in memory per server for the launch), unreachable / TLS / 5xx banner with Try again, not an Inbox server, "couldn't save the sign-in" (Keychain failure).
- **Destinations:** new -> OB02. Existing -> Mail when the server already lists businesses, else OB02. The login response has no `onboarding_step`, so "resume at the step the server reports" (D4 §1.4) is approximated by the business list.
- **Removed:** `AppleSigning`, `SystemAppleSigner`, `MockAppleSigner`, `AppleAuthRequest/Response`, `authApple`, the Apple copy, the `/v1/auth/apple` mock. There never was an entitlements file in `project.yml`.
- **Debug aids:** `-OpenSignIn new|existing|signedOut` (mock mode screenshots), `-ResetSignIn 1` (clears token and step, Debug only), `-NoDevToken 1` (ignore a dev token from Local.xcconfig). Mock passwords in `-UseMockData`: `wrong-password`, `locked-out`, `server-down`; anything else signs in.
- **Welcome AX3:** from AX1 the buttons and footer line sit in the scroll content (no longer cut off); "I already set this up" wraps to two centred lines.
- **Fonts:** the sign-in title and welcome h1 use Open Runde Bold (`Font.inboxHeadingBold`). The rest of the D4 §2.3 size/tracking table is not applied app-wide by this change.
- **Verified 2026-10-31 (simulator `Inbox iPhone 17 Pro`):** `make ios-test` 182 pass; `make ios-ui-test` 6 pass; `make ios-e2e` 10 pass against the real dev server (includes wrong password then real password `dev-password-inbox-local` -> Mail with Keychain token, and an unreachable address). Snapshots (light, dark, AX3) in `ios/InboxTests/Snapshots/OB01b-*`, `AX3-OB01b-*`, copies named per Gate 10 in `ios/docs/G10-signin-*`.
- **Not verified:** VoiceOver spoken output (labels are set per D4 §1.8 but only the UI-test accessibility labels were exercised); keyboard types are set in code and the Next/Go return keys were checked in a UI test, the URL keyboard was seen in the simulator; caret position is not preserved when toggling show/hide; "text selected" on invalid-URL focus is not implemented; real Tailscale host; physical device.
- **Known, not mine:** the AX3 Gmail "signed out" screen (`AX3-GM03`) still clips its pinned Reconnect button at the bottom in the snapshot harness (window has no safe area). The live suite depends on dev-server state (a hidden "Acme Studio" business from an earlier run broke three tests until unhidden).
