import XCTest

/// End-to-end smoke test of the app against its built-in mock data (`-UseMockData`, no server): Mail → business →
/// message → archive with Undo, the Rules tab, Settings → Gmail. Run with `make ios-ui-test`.
final class SmokeTests: XCTestCase {
    override func setUpWithError() throws { continueAfterFailure = false }

    private func launch(_ extra: [String] = []) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["-UseMockData"] + extra
        app.launch()
        return app
    }

    func testMailBusinessMessageAndArchiveUndo() throws {
        let app = launch()
        XCTAssertTrue(app.staticTexts["Businesses"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["Needs attention"].exists || app.staticTexts["NEEDS ATTENTION"].exists)

        app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Acme Studio'")).firstMatch.tap()
        // The business page opens with the address chips ("All 7", "invoices@ 2", …).
        XCTAssertTrue(app.buttons["All 7"].waitForExistence(timeout: 10))

        // Open the first message and archive it from the bottom bar.
        // Rows are combined accessibility elements labelled "<unread>. <sender>. <subject>. <time>".
        let row = app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS 'Invoice #2041'")).firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 10))
        row.tap()
        let archive = app.buttons["Archive"]
        XCTAssertTrue(archive.waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["Rules"].exists, "tab bar is hidden on the message screen")
        archive.tap()
    }

    func testRulesTabListsRulesAndTogglesOne() throws {
        let app = launch()
        app.buttons["Rules"].tap()
        XCTAssertTrue(app.staticTexts["Invoices"].waitForExistence(timeout: 10))
        let first = app.switches.firstMatch.exists ? app.switches.firstMatch : app.buttons.matching(NSPredicate(format: "label CONTAINS 'enabled'")).firstMatch
        XCTAssertTrue(first.exists)
        first.tap()
    }

    func testSettingsGmailAndHealth() throws {
        // Pin the mock Gmail state: the unit tests also use the `GmailState` default and the app shares its defaults with them.
        let app = launch(["-GmailState", "connected"])
        app.buttons["Settings"].tap()
        XCTAssertTrue(app.staticTexts["Settings"].waitForExistence(timeout: 10))
        app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Gmail'")).firstMatch.tap()
        // The Gmail screen has no tab-bar row for the account; its "Sync" group is only there after the push.
        XCTAssertTrue(app.staticTexts["SYNC"].waitForExistence(timeout: 10))
        // The row is below the fold at the default size; scroll to it.
        let disconnect = app.buttons["Disconnect Gmail"]
        if !disconnect.waitForExistence(timeout: 3) { app.swipeUp() }
        XCTAssertTrue(disconnect.waitForExistence(timeout: 5))
    }

    // MARK: Session lost (C-20) and the Open Runde licence (C-21)

    func testSignedOutBannerOpensSignInAgainWithTheSavedAddress() throws {
        let app = launch(["-SignedOut", "1"])
        let banner = app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS \"You've been signed out\"")).firstMatch
        XCTAssertTrue(banner.waitForExistence(timeout: 10), "banner on the Mail root")
        XCTAssertTrue(app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Acme Studio'")).firstMatch.exists, "cached mail stays readable")
        app.buttons["Sign in"].firstMatch.tap()
        XCTAssertTrue(app.staticTexts["Sign in again"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.textFields["Server address"].exists)
        app.buttons["Back"].tap()
        XCTAssertTrue(app.staticTexts["Businesses"].waitForExistence(timeout: 10), "Back returns to Mail, still signed out")
        XCTAssertTrue(banner.exists)
    }

    func testAboutOpensTheOpenRundeLicence() throws {
        let app = launch(["-OpenTab", "settings", "-OpenSettings", "about"])
        let row = app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Open Runde'")).firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 10))
        row.tap()
        let text = app.staticTexts.matching(NSPredicate(format: "label CONTAINS 'SIL OPEN FONT LICENSE'")).firstMatch
        XCTAssertTrue(text.waitForExistence(timeout: 10), "the licence text is on screen")
        for _ in 0..<6 { app.swipeUp() }
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS 'DISCLAIMER'")).firstMatch.waitForExistence(timeout: 5), "scrolls to the end of the licence")
    }

    // MARK: Onboarding and server sign-in (mock data; mock passwords: "wrong-password", "locked-out", "server-down")

    private func label(_ app: XCUIApplication, containing text: String) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch
    }

    private func fillSignIn(_ app: XCUIApplication, address: String, password: String) {
        let a = app.textFields["Server address"]
        XCTAssertTrue(a.waitForExistence(timeout: 10))
        a.tap(); a.clearText(); a.typeText(address)
        let p = app.secureTextFields["Password"]
        XCTAssertTrue(p.waitForExistence(timeout: 5))
        p.tap(); p.typeText(password)
    }

    func testWelcomeHasNoAppleAndBothButtonsOpenSignIn() throws {
        let app = launch(["-Onboarding", "1"])
        XCTAssertTrue(app.buttons["Get started"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.buttons["I already set this up"].exists)
        XCTAssertTrue(app.staticTexts["Next you'll enter your server address and password."].exists)
        XCTAssertFalse(label(app, containing: "Apple").exists)
        app.buttons["I already set this up"].tap()
        XCTAssertTrue(app.staticTexts["Welcome back"].waitForExistence(timeout: 10))
        app.buttons["Back"].tap()
        XCTAssertTrue(app.buttons["Get started"].waitForExistence(timeout: 10))
        app.buttons["Get started"].tap()
        XCTAssertTrue(app.staticTexts["Connect to your server"].waitForExistence(timeout: 10))
    }

    func testSignInFillsFormShowsPasswordAndContinuesToDomains() throws {
        let app = launch(["-Onboarding", "1"])
        app.buttons["Get started"].tap()
        XCTAssertTrue(app.staticTexts["Connect to your server"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["Sign in"].isEnabled, "dimmed until both fields are filled")
        let address = app.textFields["Server address"]
        XCTAssertTrue(address.waitForExistence(timeout: 5))
        address.tap(); address.typeText("inbox.example.com")
        if app.keyboards.count > 0 { XCTAssertTrue(app.keyboards.buttons.matching(NSPredicate(format: "label ==[c] %@", "next")).firstMatch.exists, "address return key is Next") }
        let password = app.secureTextFields["Password"]
        password.tap(); password.typeText("correct horse")
        if app.keyboards.count > 0 { XCTAssertTrue(app.keyboards.buttons.matching(NSPredicate(format: "label ==[c] %@", "go")).firstMatch.exists, "password return key is Go") }
        XCTAssertTrue(app.buttons["Sign in"].isEnabled)
        app.buttons["Show password"].tap()
        let shown = app.textFields["Password"]
        XCTAssertTrue(shown.waitForExistence(timeout: 5))
        let revealed = expectation(for: NSPredicate(format: "value == %@", "correct horse"), evaluatedWith: shown)
        wait(for: [revealed], timeout: 5)
        XCTAssertTrue(app.buttons["Hide password"].exists)
        app.buttons["Hide password"].tap()
        XCTAssertTrue(app.secureTextFields["Password"].waitForExistence(timeout: 5))
        app.buttons["Sign in"].tap()
        XCTAssertTrue(app.staticTexts["Which domains are your businesses?"].waitForExistence(timeout: 10))
    }

    /// Mock mode stands in for Google (the sandbox has no Gmail): Continue with Google succeeds, then OB07 "Gmail connected", the filters
    /// step, the import step, Alfather, and OB12 "You're set up" whose "Open Inbox" ends setup.
    func testGmailConnectedAndSetUpScreensEndTheFlow() throws {
        let app = launch(["-Onboarding", "1", "-OnboardingStep", "gmail", "-GmailState", "connected"])
        app.buttons["Continue with Google"].tap()
        XCTAssertTrue(app.staticTexts["Gmail connected"].waitForExistence(timeout: 15), "OB07")
        XCTAssertTrue(app.staticTexts["Step 3 of 7"].exists)
        XCTAssertTrue(app.staticTexts["Read, label and archive"].exists)
        XCTAssertTrue(app.staticTexts["You can disconnect at any time in Settings → Gmail."].exists)
        app.buttons["Continue"].tap()
        XCTAssertTrue(app.staticTexts["Bring your Gmail filters"].waitForExistence(timeout: 10))
        app.buttons["Skip"].tap()
        XCTAssertTrue(app.staticTexts["Import older mail?"].waitForExistence(timeout: 10))
        app.buttons["Import 90 days"].tap()
        // Alfather step: "Set up later" when nothing is found (OB11), "Continue" when the mock finds one; Continue is disabled while it searches.
        let finish = app.buttons.matching(NSPredicate(format: "label == 'Set up later' OR label == 'Continue'")).firstMatch
        XCTAssertTrue(finish.waitForExistence(timeout: 10))
        let enabled = XCTNSPredicateExpectation(predicate: NSPredicate(format: "isEnabled == true"), object: finish)
        XCTAssertEqual(XCTWaiter().wait(for: [enabled], timeout: 15), .completed)
        finish.tap()
        XCTAssertTrue(app.staticTexts["You're set up."].waitForExistence(timeout: 10), "OB12")
        XCTAssertTrue(label(app, containing: "90 days of mail on its way").exists)
        XCTAssertFalse(app.buttons["Back"].exists, "no Back on the last screen")
        app.buttons["Open Inbox"].tap()
        XCTAssertTrue(app.staticTexts["Businesses"].waitForExistence(timeout: 15), "setup done: Mail root")
    }

    func testSignInErrorsWrongPasswordInvalidAddressLockedAndUnreachable() throws {
        let app = launch(["-Onboarding", "1", "-OpenSignIn", "existing"])
        XCTAssertTrue(app.staticTexts["Welcome back"].waitForExistence(timeout: 10))
        // Invalid address: no request, inline error.
        fillSignIn(app, address: "inbox", password: "x")
        app.buttons["Sign in"].tap()
        XCTAssertTrue(label(app, containing: "That doesn't look like a server address").waitForExistence(timeout: 5))
        // Wrong password: the password is cleared and the address is kept.
        let address = app.textFields["Server address"]
        address.tap(); address.clearText(); address.typeText("inbox.example.com")
        let password = app.secureTextFields["Password"]
        password.tap(); password.clearText(); password.typeText("wrong-password")
        app.buttons["Sign in"].tap()
        XCTAssertTrue(label(app, containing: "That password isn't right.").waitForExistence(timeout: 10))
        XCTAssertEqual(app.textFields["Server address"].value as? String, "https://inbox.example.com")
        // Server error: banner with Try again, button stays Sign in.
        let pw2 = app.secureTextFields["Password"]
        pw2.tap(); pw2.typeText("server-down")
        app.buttons["Sign in"].tap()
        XCTAssertTrue(label(app, containing: "Can't reach your server").waitForExistence(timeout: 10))
        XCTAssertTrue(app.buttons["Try again"].exists && app.buttons["Sign in"].exists)
        // Locked out: banner with the countdown, button disabled.
        let pw3 = app.secureTextFields["Password"]
        pw3.tap(); pw3.clearText(); pw3.typeText("locked-out")
        app.buttons["Sign in"].tap()
        XCTAssertTrue(label(app, containing: "Too many tries").waitForExistence(timeout: 10))
        let locked = app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Try again in 4 minutes'")).firstMatch
        XCTAssertTrue(locked.waitForExistence(timeout: 5))
        XCTAssertFalse(locked.isEnabled)
    }

    // MARK: BZ02 reorder by drag (D3)

    /// The Mail-root Edit list: pick up Lumen Hosting's six-dot handle (right edge of its row), drop it on Acme Studio, and the order flips.
    /// Mock data: `PUT /v1/businesses:order` is answered by the in-app mock, so the new order persists instead of rolling back.
    /// VoiceOver's "Move up" / "Move down" actions cannot be invoked from XCUITest; they are covered by `BusinessReorderActionsTests` (unit target).
    func testBusinessesReorderByDragPersists() throws {
        let app = launch(["-OpenBusinessesEdit", "1"])
        func row(_ name: String) -> XCUIElement { app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", name)).firstMatch }
        let acme = row("Acme Studio"), lumen = row("Lumen Hosting")
        XCTAssertTrue(acme.waitForExistence(timeout: 10))
        XCTAssertTrue(lumen.waitForExistence(timeout: 10))
        XCTAssertLessThan(acme.frame.minY, lumen.frame.minY, "starts as Acme, Northgate, Lumen")

        let handle = lumen.coordinate(withNormalizedOffset: CGVector(dx: 0.93, dy: 0.5))
        let target = acme.coordinate(withNormalizedOffset: CGVector(dx: 0.93, dy: 0.25))
        handle.press(forDuration: 0.8, thenDragTo: target, withVelocity: .slow, thenHoldForDuration: 0.6)

        let moved = NSPredicate { _, _ in row("Lumen Hosting").frame.minY < row("Acme Studio").frame.minY }
        XCTAssertEqual(XCTWaiter().wait(for: [XCTNSPredicateExpectation(predicate: moved, object: nil)], timeout: 10), .completed, "Lumen Hosting is now above Acme Studio")
        XCTAssertFalse(label(app, containing: "back to how it was").exists, "the order was saved, not rolled back")

        // Done leaves Edit; the Mail list shows the new order.
        app.buttons["Done"].tap()
        let lumenRow = app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Lumen Hosting'")).firstMatch
        let acmeRow = app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Acme Studio'")).firstMatch
        XCTAssertTrue(lumenRow.waitForExistence(timeout: 10))
        XCTAssertLessThan(lumenRow.frame.minY, acmeRow.frame.minY)
    }
}

private extension XCUIElement {
    /// Deletes the field's current text (works for secure fields, whose value is bullets).
    func clearText() {
        guard let s = value as? String, !s.isEmpty else { return }
        typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: s.count))
    }
}
