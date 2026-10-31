#!/bin/bash
# Reshoots every ios/docs T*/S* image (light + dark) from the mock-data app with scripts/shoot.sh, so they match the current fonts and screens.
# Usage: scripts/reshoot-docs.sh            (build and install the app first: make ios-build; uses the "Inbox iPhone 17 Pro" simulator)
# Not reshot: ios/docs/LIVE-businesses.png (a capture against the live dev server) and S7-rules-editor-unrouted (needs a tap inside the app).
set -e
cd "$(dirname "$0")/.."
DEV=${SIM_DEVICE:-"Inbox iPhone 17 Pro"}
shoot() { scripts/shoot.sh "$@"; }
ax3() {
  xcrun simctl ui "$DEV" content_size accessibility-extra-extra-extra-large
  shoot "$@"
  xcrun simctl ui "$DEV" content_size large
}
shoot T11-businesses
shoot T13-business-page -OpenBusiness 1
shoot S7-business-page -OpenBusiness 1
shoot S1-businesses-edit -OpenBusinessesEdit 1
shoot T14-message-invoice -OpenMessage 1
shoot T14-message-thread -OpenMessage 2
shoot T14-message-images-blocked -OpenMessage 13
shoot T15-search-idle -OpenSearch 1
shoot T15-search-results -OpenSearchQuery invoice
shoot T15-search-scoped -OpenSearchQuery invoice -OpenSearchScope 1
shoot T17-rules -OpenTab rules
shoot T17-unrouted -OpenUnrouted 1
shoot T17-failed -OpenFailed 1
shoot T17-failed-detail -OpenFailedDetail 1
shoot T21-settings -OpenTab settings
shoot T24-settings -OpenTab settings
shoot T21-gmail-connected -OpenTab settings -OpenGmail 1 -GmailState connected
shoot T21-gmail-catching-up -OpenTab settings -OpenGmail 1 -GmailState catching_up
shoot T21-gmail-rate-limited -OpenTab settings -OpenGmail 1 -GmailState rate_limited
shoot T21-gmail-signed-out -OpenTab settings -OpenGmail 1 -GmailState needs_reauth
shoot T21-gmail-not-connected -OpenTab settings -OpenGmail 1 -GmailState not_connected
shoot T21-mail-signed-out-banner -GmailState needs_reauth
shoot T21-import-filters -OpenTab rules -OpenImport 1
shoot T23-ob01-welcome -Onboarding 1
shoot T23-ob02-domains -Onboarding 1 -OnboardingStep domains
shoot T23-ob05-gmail -Onboarding 1 -OnboardingStep gmail
shoot T23-ob08-filters -Onboarding 1 -OnboardingStep filters
shoot T23-ob09-backfill -Onboarding 1 -OnboardingStep backfill
shoot T23-ob10-alfather -Onboarding 1 -OnboardingStep alfather
shoot T24-alfather-setup -OpenTab settings -OpenSettings alfather -AlfatherState not_paired
shoot T24-alfather-connected -OpenTab settings -OpenSettings alfather -AlfatherState connected
shoot T24-alfather-unreachable -OpenTab settings -OpenSettings alfather -AlfatherState unreachable
shoot T24-appearance -OpenTab settings -OpenSettings appearance
shoot T24-businesses -OpenTab settings -OpenSettings businesses
shoot T24-business-detail -OpenTab settings -OpenSettings business
shoot T24-routing-problem -OpenTab settings -OpenSettings routing
shoot T24-health-needs-you -OpenTab settings -OpenSettings health -HealthState needs_you
shoot T24-health-ok -OpenTab settings -OpenSettings health -HealthState ok
shoot T24-privacy -OpenTab settings -OpenSettings privacy
shoot T27-delete-everything -OpenTab settings -OpenSettings privacy -OpenDeleteSheet 1
shoot T26-import-setup -OpenTab settings -OpenSettings import -BackfillState none
shoot T26-import-running -OpenTab settings -OpenSettings import -BackfillState running
shoot T26-import-paused -OpenTab settings -OpenSettings import -BackfillState paused
shoot T26-import-done -OpenTab settings -OpenSettings import -BackfillState done
shoot T26-mail-import-banner -BackfillState running
ax3 S5-ax3-business-page -OpenBusiness 1
ax3 S5-ax3-message -OpenMessage 1
ax3 S5-ax3-thread -OpenMessage 2
