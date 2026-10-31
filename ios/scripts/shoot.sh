#!/bin/bash
# Usage: scripts/shoot.sh <name> [launch args...]   -> docs/<name>-light.png and docs/<name>-dark.png (mock data, iPhone 17 Pro)
set -e
cd "$(dirname "$0")/.."
DEV=${SIM_DEVICE:-"Inbox iPhone 17 Pro"}
APP=$(ls -dt ~/Library/Developer/Xcode/DerivedData/Inbox-*/Build/Products/Debug-iphonesimulator/Inbox.app | head -1)
NAME=$1; shift
xcrun simctl boot "$DEV" 2>/dev/null || true
xcrun simctl install "$DEV" "$APP"
xcrun simctl status_bar "$DEV" override --time 9:41 --batteryState charged --batteryLevel 100 --cellularBars 4 --wifiBars 3 >/dev/null 2>&1 || true
for mode in light dark; do
  xcrun simctl ui "$DEV" appearance $mode
  sleep 2
  # first launch after an appearance switch is sometimes swallowed by SpringBoard; launch twice
  xcrun simctl launch --terminate-running-process "$DEV" com.casprine.inbox -UseMockData -AppleLocale en_US -AppleLanguages "(en)" "$@" >/dev/null
  sleep 3
  xcrun simctl launch --terminate-running-process "$DEV" com.casprine.inbox -UseMockData -AppleLocale en_US -AppleLanguages "(en)" "$@" >/dev/null
  sleep 5
  xcrun simctl io "$DEV" screenshot "docs/$NAME-$mode.png" >/dev/null 2>&1
done
