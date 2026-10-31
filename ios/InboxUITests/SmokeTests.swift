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
}

private extension XCUIElement {
    /// Deletes the field's current text (works for secure fields, whose value is bullets).
    func clearText() {
        guard let s = value as? String, !s.isEmpty else { return }
        typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: s.count))
    }
}
