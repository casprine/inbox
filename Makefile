DB_URL ?= postgres://inbox:inbox@localhost:54329/inbox?sslmode=disable
S3_URL ?= http://localhost:9009
COMPOSE = docker compose -f deploy/docker-compose.yml

.PHONY: build test test-integration lint up down
build:
	cd server && go build ./...
test:
	cd server && go test ./...
test-integration: up
	cd server && TEST_DATABASE_URL='$(DB_URL)' TEST_S3_ENDPOINT='$(S3_URL)' go test -count=1 -p 1 ./...
lint:
	cd server && go vet ./... && test -z "$$(gofmt -l .)"
up:
	$(COMPOSE) up -d --wait
down:
	$(COMPOSE) down

.PHONY: docker-build edge-test ci e2e dev-server dev-server-start dev-server-stop
docker-build:
	docker build -f deploy/Dockerfile -t inbox-server .
edge-test:
	cd edge && npm ci && npm test && npx tsc --noEmit
# What CI runs for the server and the edge worker (the iOS job runs on a macOS runner).
ci: lint test-integration edge-test
e2e:
	scripts/e2e.sh
dev-server:
	scripts/dev-server.sh run
dev-server-start:
	scripts/dev-server.sh start
dev-server-stop:
	scripts/dev-server.sh stop
# Deliver test mail through the real Worker harness into the running dev server; options TO FROM SUBJECT BODY EML COUNT SAMPLE (docs/DEV_SERVER.md).
.PHONY: send-test-mail
send-test-mail:
	@node --experimental-strip-types --no-warnings scripts/send-test-mail.mjs

# Our own simulator: other projects on this Mac share "iPhone 17 Pro", and their UI runs steal the foreground (blank snapshots).
IOS_SIM ?= Inbox iPhone 17 Pro
IOS_DEST ?= platform=iOS Simulator,name=$(IOS_SIM)
.PHONY: ios-local-config ios-device-build ios-sim ios-gen ios-build ios-test ios-ui-test ios-e2e ios-record-snapshots
# Create (once) and boot the dedicated iOS simulator.
ios-sim:
	@xcrun simctl list devices | grep -q "$(IOS_SIM) (" || xcrun simctl create "$(IOS_SIM)" "iPhone 17 Pro" $$(xcrun simctl list runtimes | grep -o 'com.apple.CoreSimulator.SimRuntime.iOS-[0-9-]*' | tail -1) > /dev/null
	@xcrun simctl boot "$(IOS_SIM)" 2>/dev/null || true
# Writes ios/Local.xcconfig (server URL + dev token for Debug builds on a phone) from this Mac's LAN IP.
ios-local-config:
	scripts/ios-local-config.sh
ios-device-build: ios-gen
	xcodebuild -project ios/Inbox.xcodeproj -scheme 'Inbox Device' -destination 'generic/platform=iOS' build
ios-gen:
	cd ios && xcodegen generate
ios-build: ios-gen ios-sim
	xcodebuild -project ios/Inbox.xcodeproj -scheme Inbox -destination '$(IOS_DEST)' build
ios-test: ios-gen ios-sim
	xcodebuild -project ios/Inbox.xcodeproj -scheme Inbox -destination '$(IOS_DEST)' -skip-testing:InboxUITests -skip-testing:InboxLiveTests test
# UI smoke tests (mock data, no server needed).
ios-ui-test: ios-gen ios-sim
	xcodebuild -project ios/Inbox.xcodeproj -scheme Inbox -destination '$(IOS_DEST)' -only-testing:InboxUITests -skip-testing:InboxLiveTests test
# Live suite: starts the real dev server (detached, port 18090), runs the opt-in InboxLiveTests UI tests against it
# with the dev token, always stops the server again. The tests live in ios/ (Swift agent); until that target exists
# xcodebuild reports it as missing. Environment reaches the tests through the TEST_RUNNER_ prefix.
ios-e2e: ios-gen ios-sim
	scripts/dev-server.sh start
	TEST_RUNNER_INBOX_API_BASE_URL=http://127.0.0.1:18090 TEST_RUNNER_INBOX_DEV_TOKEN=dev-token-inbox-local-0000 \
	  xcodebuild -project ios/Inbox.xcodeproj -scheme Inbox -destination '$(IOS_DEST)' -only-testing:InboxLiveTests test; \
	  rc=$$?; scripts/dev-server.sh stop; exit $$rc
# Re-record the snapshot baselines in ios/InboxTests/Snapshots after an intended UI change (iPhone 17 Pro, iOS 26.5).
ios-record-snapshots: ios-gen ios-sim
	TEST_RUNNER_RECORD_SNAPSHOTS=1 xcodebuild -project ios/Inbox.xcodeproj -scheme Inbox -destination '$(IOS_DEST)' -only-testing:InboxTests/SnapshotTests test
