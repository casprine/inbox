# Run Inbox on your iPhone (demo data)

You'll run the real server on your Mac and the app on your phone, both on the same Wi-Fi. The app opens straight into Mail with the demo businesses. Plan for about 15 minutes the first time.

## Before you start
- Mac and iPhone on the **same Wi-Fi** (not a guest network, not a VPN).
- Xcode installed, signed in with your Apple ID for code signing only (Xcode → Settings → Accounts). The project uses team `2QG5WSN24S` (Edward Assempah). If you sign in with a different team, change `DEVELOPMENT_TEAM` in `ios/project.yml`, then run `make ios-gen`.
- Docker running (the server uses it for Postgres and MinIO).

## Steps
1. **Turn on Developer Mode on the phone.** Settings → Privacy & Security → Developer Mode → On. The phone restarts; confirm "Turn On" after the restart and enter your passcode. (If the switch is missing, plug the phone into the Mac with Xcode open once; it then appears.)
2. **Connect the phone with a cable and tap "Trust This Computer"** on the phone, then enter your passcode. In Xcode, Window → Devices and Simulators should list your phone (e.g. "Midgard") with no warnings. A wait of a minute for "preparing the device" is normal.
3. **Start the server so the phone can reach it.** In a terminal at the repo root:
   ```bash
   LAN=1 make dev-server-start
   ```
   It prints `base URL : http://<your-mac-ip>:18090`. Plain `make dev-server` listens on this Mac only and the phone can't reach it.
4. **Write the app's config for your Mac's address:**
   ```bash
   make ios-local-config
   ```
   This creates `ios/Local.xcconfig` (git-ignored) with the server URL and the public dev token. Re-run it whenever your Mac's IP changes (new Wi-Fi, reboot), then rebuild the app.
5. **Open the project and generate it:**
   ```bash
   make ios-gen && open ios/Inbox.xcodeproj
   ```
6. **In Xcode:** pick scheme **Inbox Device** (an optimised build; the plain **Inbox** scheme is a Debug build and scrolls noticeably slower on a phone) and your phone as the run destination (top bar). Select the Inbox target → Signing & Capabilities: "Automatically manage signing" on, Team = your team. The first build registers the app ID and creates a profile for you; this needs you to be signed in and online.
7. **Press Run (⌘R).** The first build takes a few minutes.
8. **If the phone says "Untrusted Developer":** Settings → General → VPN & Device Management → your Apple ID → Trust. Then open the app again.
9. **Accept the local network prompt** ("Inbox would like to find and connect to devices on your local network") → **Allow**. Without it the app can't reach the server.
10. **Done:** the app opens in Mail with demo data.

## Using your real server instead of the demo data
The demo build above skips sign-in with a dev token. To sign in the way the real app does: leave `INBOX_DEV_TOKEN` out of `ios/Local.xcconfig` (or launch with `-NoDevToken 1`), open the app, tap **Get started** or **I already set this up**, then enter:
- **Server address:** the Tailscale Serve host of your Mac mini, for example `mini.tail1234.ts.net` (https, no port, no path; the app adds `https://` and strips anything after the host). Plain `http://` is only accepted for localhost, `*.local` and private LAN addresses (so `http://192.168.x.x:18090` for the dev server works).
- **Password:** the server's `OWNER_PASSWORD`. The dev server accepts `dev-password-inbox-local`.
The phone must be connected to **Tailscale** (same tailnet as the Mac mini) to reach a `.ts.net` address; Wi-Fi alone is not enough. The password is never stored on the phone: only the server address and the revocable device token it returns (kept in the Keychain). After five wrong passwords the server pauses sign-in and the button counts down; it is per client address.

## What to try
- Mail: open a business (e.g. Acme Studio), open a message, archive it and tap Undo.
- With the server running, `make send-test-mail` and pull to refresh on the phone.
- Pull to refresh; open Needs attention and act on an item.
- Rules tab: toggle a rule; open one to edit it.
- Search: find "invoice".
- Settings: look around; try the largest Dynamic Type size and dark mode.

## What will not work on this build
- **Sign-in screen:** with the dev token in `Local.xcconfig` the app skips it. Remove the token to try it (see above).
- **Gmail connect:** needs Google OAuth credentials the dev server doesn't have. Real mail won't arrive.
- **Cloudflare / real inbound email:** nothing is routed to your Mac; the demo mail is seeded.
- **Alfather:** pairing needs the real device; expect it to find nothing.
- **Fonts:** Open Runde is bundled (SIL Open Font License); not yet checked on a real device.
- **Free-team limit:** with a free Apple ID the app stops launching after **7 days**. Run it from Xcode again to renew. Apps on a free team are also limited to a few installs per week.
- The dev token is public and the server listens on your whole network while `LAN=1` is on. Use trusted Wi-Fi only, and stop it afterwards: `make dev-server-stop`.

## Troubleshooting
1. **App shows a connection error / spinner forever ("can't reach server").**
   - On the phone open Safari and go to `http://<mac-ip>:18090/healthz`. You should see `{"status":"ok"}`. If not: the server isn't running with `LAN=1`, the Mac IP changed (re-run `make ios-local-config` and rebuild), the phone is on another network, or a Mac firewall blocks `inboxd` (System Settings → Network → Firewall → allow incoming).
   - If Safari works but the app doesn't, see 3.
   - Port 18090 busy with another project's server: run with `PORT=18091 LAN=1 make dev-server-start` and `PORT=18091 make ios-local-config`.
2. **"Untrusted Developer" or the app won't open.** Settings → General → VPN & Device Management → Trust your Apple ID. After 7 days on a free team, run again from Xcode.
3. **Local network access denied.** Settings → Privacy & Security → Local Network → turn **Inbox** on (or Settings → Inbox). Relaunch the app. If Inbox isn't listed, delete the app and run again to get the prompt.
4. **Signing error** ("No profiles found", "Failed to register bundle identifier", "No account for team").
   - Sign in under Xcode → Settings → Accounts and select the team on the target.
   - "Bundle identifier not available": change `PRODUCT_BUNDLE_IDENTIFIER` in `ios/project.yml` to something unique (e.g. `com.<you>.inbox`), `make ios-gen`.
   - Phone not listed or "not eligible": unlock it, reconnect, enable Developer Mode (step 1).
5. **Crash on launch or odd compile errors after a change (stale module).** Quit Xcode, then:
   ```bash
   rm -rf ~/Library/Developer/Xcode/DerivedData/Inbox-*/Build && make ios-gen
   ```
   Reopen and Run again (Product → Clean Build Folder also works).

## If the phone gets signed out
When the server revokes this phone's token (or it expires) the app clears it and shows "You've been signed out" on Mail; your mail stays readable. Tap Sign in: the server address is already filled in, type the server password. Nothing else is lost. (The dev token from `Local.xcconfig` is only used at launch; after signing in with the password the phone uses its own token until the next launch.)
