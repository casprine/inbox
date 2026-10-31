## 2026-10-12 S-7 iOS CI job for .github/workflows/ci.yml (please merge in B-7)
Add (or replace the existing ios job with) this job; it is verified locally except for the runner itself (never ran on GitHub):
```yaml
  ios:
    runs-on: macos-15            # needs an Xcode with the iOS 26 SDK; if the runner lacks it, pin `xcode-version` below
    steps:
      - uses: actions/checkout@v4
      - uses: maxim-lobanov/setup-xcode@v1
        with: { xcode-version: latest-stable }
      - run: brew install xcodegen
      - name: Unit, feature, contract and snapshot tests
        run: make ios-test            # creates its own simulator "Inbox iPhone 17 Pro" (IOS_SIM=… to rename)
      - name: UI smoke tests (mock data)
        run: make ios-ui-test
      - name: Unsigned Release archive
        run: cd ios && xcodegen generate && xcodebuild archive -project Inbox.xcodeproj -scheme Inbox -configuration Release -destination 'generic/platform=iOS' -archivePath build/Inbox.xcarchive CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO
      - uses: actions/upload-artifact@v4
        with: { name: Inbox.xcarchive, path: ios/build/Inbox.xcarchive }
```
Notes: snapshot baselines (ios/InboxTests/Snapshots) were recorded on iPhone 17 Pro / iOS 26.5; if the runner's runtime differs, the snapshot tests fail until `make ios-record-snapshots` is re-run there (set `IOS_SIM_RUNTIME` is not supported; the Makefile uses the newest installed iOS runtime). `make ios-e2e` (live suite) is not part of this job. `make ios-sim` needs `xcrun simctl create` rights (fine on hosted runners).

## 2026-10-14 — `LAN=1` for the dev server (swift agent, small change)
`scripts/dev-server.sh` now takes `LAN=1` (`LAN=1 make dev-server-start`): binds `0.0.0.0` and uses the Mac's Wi-Fi/Ethernet IP as `PUBLIC_BASE_URL`, so a phone on the same network can reach it. Default stays `127.0.0.1`. The readiness probe follows the LAN IP. Observed: another project's `api` process holds `127.0.0.1:18090` on this Mac; the plain (loopback) mode then refuses with "not our server" — use `PORT=...`. Guide: `docs/RUN_ON_DEVICE.md`.
