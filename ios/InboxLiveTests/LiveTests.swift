import XCTest

/// Live end-to-end suite: the real app (debug build, dev token) against the real dev server with its demo data.
/// Opt-in: `make ios-e2e` starts the server and passes `INBOX_API_BASE_URL` / `INBOX_DEV_TOKEN`; without them every test skips.
/// Tests arrange and verify through the server's own API and put back what they change, so the suite can be re-run.
final class LiveTests: XCTestCase {
    private var base: URL!
    private var token = ""

    override func setUpWithError() throws {
        continueAfterFailure = false
        let env = ProcessInfo.processInfo.environment
        guard let b = env["INBOX_API_BASE_URL"], let t = env["INBOX_DEV_TOKEN"], let url = URL(string: b) else {
            throw XCTSkip("Set INBOX_API_BASE_URL and INBOX_DEV_TOKEN (make ios-e2e does).")
        }
        base = url; token = t
    }

    // MARK: Helpers

    private func launch() -> XCUIApplication {
        let app = XCUIApplication()
        app.launchEnvironment["INBOX_API_BASE_URL"] = base.absoluteString
        app.launchEnvironment["INBOX_DEV_TOKEN"] = token
        app.launch()
        return app
    }

    /// Synchronous JSON call against the dev server (arrange / verify).
    @discardableResult
    private func api(_ method: String, _ path: String, body: [String: Any]? = nil) throws -> Any? {
        var req = URLRequest(url: URL(string: base.absoluteString + path)!)
        req.httpMethod = method
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue(UUID().uuidString, forHTTPHeaderField: "Idempotency-Key")
        if let body { req.httpBody = try JSONSerialization.data(withJSONObject: body); req.setValue("application/json", forHTTPHeaderField: "Content-Type") }
        var out: (Data?, URLResponse?, Error?) = (nil, nil, nil)
        let sem = DispatchSemaphore(value: 0)
        URLSession.shared.dataTask(with: req) { out = ($0, $1, $2); sem.signal() }.resume()
        sem.wait()
        if let e = out.2 { throw e }
        guard let data = out.0, !data.isEmpty else { return nil }
        return try? JSONSerialization.jsonObject(with: data)
    }

    private func messages(business: String? = nil) throws -> [[String: Any]] {
        let q = business.map { "&business=\($0)" } ?? ""
        return ((try api("GET", "/v1/messages?limit=100\(q)") as? [String: Any])?["messages"] as? [[String: Any]]) ?? []
    }

    private func element(_ app: XCUIApplication, containing text: String) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch
    }

    private func openMail(_ app: XCUIApplication) {
        XCTAssertTrue(app.staticTexts["Businesses"].waitForExistence(timeout: 15), "should land on Mail with the dev token")
    }

    // MARK: Tests

    func testLaunchesStraightToMailWithTheDevToken() throws {
        let app = launch()
        openMail(app)
        XCTAssertFalse(app.buttons["Get started"].exists, "no onboarding when a token is seeded")
        XCTAssertTrue(app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Acme Studio'")).firstMatch.waitForExistence(timeout: 10))
        XCTAssertTrue(element(app, containing: "Needs attention").exists || app.staticTexts["NEEDS ATTENTION"].exists)
    }

    func testOpenBusinessThenMessageWithHTMLBody() throws {
        let app = launch(); openMail(app)
        app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Acme Studio'")).firstMatch.tap()
        let row = app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS[c] 'invoice'")).firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 15))
        row.tap()
        XCTAssertTrue(app.buttons["Archive"].waitForExistence(timeout: 15), "message screen with its bottom bar")
        XCTAssertFalse(app.buttons["Rules"].exists, "tab bar is hidden on the message screen")
        // The body is a web view; its text shows up as static text once loaded.
        XCTAssertTrue(app.webViews.firstMatch.waitForExistence(timeout: 15) || app.staticTexts.matching(NSPredicate(format: "label CONTAINS 'Thanks'")).firstMatch.exists)
    }

    func testSwipeArchiveThenUndoRestoresTheMessage() throws {
        let acme = try XCTUnwrap(((try api("GET", "/v1/businesses") as? [String: Any])?["businesses"] as? [[String: Any]])?.first { ($0["name"] as? String) == "Acme Studio" }?["id"] as? String)
        let before = try messages(business: acme).filter { ($0["archived"] as? Bool) == false }.count
        let app = launch(); openMail(app)
        app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Acme Studio'")).firstMatch.tap()
        let first = app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS 'Unread' OR label CONTAINS '. '")).firstMatch
        XCTAssertTrue(first.waitForExistence(timeout: 15))
        first.swipeLeft()
        let archive = app.buttons["Archive"]
        XCTAssertTrue(archive.waitForExistence(timeout: 5)); archive.tap()
        let undo = app.buttons["Undo"]
        XCTAssertTrue(undo.waitForExistence(timeout: 5), "5 second Undo toast")
        undo.tap()
        Thread.sleep(forTimeInterval: 8)   // the server only hears about it after the 5 s hold (and not at all after Undo)
        let after = try messages(business: acme).filter { ($0["archived"] as? Bool) == false }.count
        XCTAssertEqual(before, after, "Undo must leave the message unarchived on the server")
    }

    func testSearchFindsDemoMailGroupedByBusiness() throws {
        let app = launch(); openMail(app)
        app.buttons["Search"].firstMatch.tap()
        let field = app.textFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 10))
        field.tap(); field.typeText("invoice")
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS[c] 'ACME STUDIO'")).firstMatch.waitForExistence(timeout: 15),
                      "results are grouped under the business name")
    }

    func testRuleFromAnUnroutedMessageRoutesIt() throws {
        let rulesBefore = ((try api("GET", "/v1/rules") as? [String: Any])?["rules"] as? [[String: Any]])?.count ?? 0
        let unroutedBefore = ((try api("GET", "/v1/attention") as? [String: Any])?["unrouted"] as? [String: Any])?["count"] as? Int ?? 0
        XCTAssertGreaterThan(unroutedBefore, 0, "demo data has unrouted mail")
        let app = launch(); openMail(app)
        let unrouted = app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Unrouted'")).firstMatch
        XCTAssertTrue(unrouted.waitForExistence(timeout: 15)); unrouted.tap()
        let row = app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS 'Unread' OR label CONTAINS '. '")).firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 15)); row.tap()
        let create = app.buttons["Create rule"]
        XCTAssertTrue(create.waitForExistence(timeout: 10)); create.tap()
        Thread.sleep(forTimeInterval: 3)
        let rules = (try api("GET", "/v1/rules") as? [String: Any])?["rules"] as? [[String: Any]] ?? []
        XCTAssertEqual(rules.count, rulesBefore + 1, "the rule exists on the server")
        let unroutedAfter = ((try api("GET", "/v1/attention") as? [String: Any])?["unrouted"] as? [String: Any])?["count"] as? Int ?? 0
        XCTAssertLessThan(unroutedAfter, unroutedBefore, "the message left Unrouted")
        // Put the server back: delete the rule we made (its messages go back to unrouted).
        for r in rules.prefix(max(rules.count - rulesBefore, 0)) { if let id = r["id"] as? String { try api("DELETE", "/v1/rules/\(id)") } }
    }

    func testFailedItemRetryRecoversTheMailWithACopy() throws {
        let failed = ((try api("GET", "/v1/attention/failed") as? [String: Any])?["messages"] as? [[String: Any]]) ?? []
        let retriable = failed.first { ($0["failure_reason"] as? String) == "store_failed" && ($0["has_raw"] as? Bool) == true }
        try XCTSkipIf(retriable == nil, "the demo seed has no retriable failed item (re-run make dev-server-start)")
        let app = launch(); openMail(app)
        let failedRow = app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Failed'")).firstMatch
        XCTAssertTrue(failedRow.waitForExistence(timeout: 15)); failedRow.tap()
        // The failed rows have no sender or subject; the retriable one is the n-th "Couldn't store." row in the server's order.
        let storeFailed = failed.filter { ($0["failure_reason"] as? String) == "store_failed" }
        let n = storeFailed.firstIndex { ($0["has_raw"] as? Bool) == true } ?? 0
        let rows = app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS[c] \"Couldn't store\""))
        XCTAssertTrue(rows.firstMatch.waitForExistence(timeout: 15))
        let item = rows.element(boundBy: n)
        item.tap()
        let retry = app.buttons["Retry"]
        XCTAssertTrue(retry.waitForExistence(timeout: 10)); retry.tap()
        Thread.sleep(forTimeInterval: 4)
        let after = ((try api("GET", "/v1/attention/failed") as? [String: Any])?["messages"] as? [[String: Any]]) ?? []
        XCTAssertEqual(after.count, failed.count - 1, "the retried item is no longer in Failed")
    }

    func testHealthAndGmailNotConfigured() throws {
        let app = launch(); openMail(app)
        app.buttons["Settings"].tap()
        let health = app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Health'")).firstMatch
        XCTAssertTrue(health.waitForExistence(timeout: 10)); health.tap()
        XCTAssertTrue(app.staticTexts["Mail arriving"].waitForExistence(timeout: 15), "grouped Mail arriving row from /v1/health")
        app.buttons["Back, Settings"].tap()
        app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Gmail'")).firstMatch.tap()
        XCTAssertTrue(app.staticTexts["Gmail isn't set up on your server yet"].waitForExistence(timeout: 15))
    }

    func testPrivacyRetentionChangePersistsOnTheServer() throws {
        let original = (try api("GET", "/v1/prefs") as? [String: Any])?["retention_days"]
        defer { _ = try? api("PUT", "/v1/prefs", body: ["retention_days": original is NSNull || original == nil ? NSNull() : original as Any]) }
        let app = launch(); openMail(app)
        app.buttons["Settings"].tap()
        let privacy = app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Privacy'")).firstMatch
        XCTAssertTrue(privacy.waitForExistence(timeout: 10)); privacy.tap()
        let keep = app.buttons.matching(NSPredicate(format: "label CONTAINS 'Keep stored copies'")).firstMatch
        XCTAssertTrue(keep.waitForExistence(timeout: 10)); keep.tap()
        // Menu items are buttons or menu items depending on the OS; accept either.
        let ninety = app.buttons["90 days"].exists ? app.buttons["90 days"] : app.menuItems["90 days"]
        XCTAssertTrue(ninety.waitForExistence(timeout: 5)); ninety.tap()
        Thread.sleep(forTimeInterval: 2)
        XCTAssertEqual((try api("GET", "/v1/prefs") as? [String: Any])?["retention_days"] as? Int, 90)
    }

    // MARK: Password sign-in (D-012): no dev token, real POST /v1/auth/login

    /// Launches with the server URL but no token, after wiping any earlier session, so the welcome screen shows.
    private func launchSignedOut() -> XCUIApplication {
        let app = XCUIApplication()
        app.launchEnvironment["INBOX_API_BASE_URL"] = base.absoluteString
        app.launchArguments = ["-ResetSignIn", "1", "-NoDevToken", "1"]
        app.launch()
        return app
    }

    private func typeCredentials(_ app: XCUIApplication, password: String) {
        let address = app.textFields["Server address"]
        XCTAssertTrue(address.waitForExistence(timeout: 10))
        address.tap()
        // "I already set this up" pre-fills the address remembered from an earlier sign-in; replace it.
        if let old = address.value as? String, !old.isEmpty, old != "inbox.example.com" { address.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: old.count)) }
        address.typeText(base.absoluteString)
        let field = app.secureTextFields["Password"]
        field.tap(); field.typeText(password)
    }

    func testWrongPasswordIsRejectedThenTheRealPasswordSignsIn() throws {
        let app = launchSignedOut()
        XCTAssertTrue(app.buttons["I already set this up"].waitForExistence(timeout: 15))
        app.buttons["I already set this up"].tap()
        typeCredentials(app, password: "definitely-not-the-password")
        app.buttons["Sign in"].tap()
        XCTAssertTrue(element(app, containing: "That password isn't right.").waitForExistence(timeout: 15))
        XCTAssertFalse(app.staticTexts["Businesses"].exists)
        // The server remembered nothing but the failed attempt; the right password gets in (this server already has businesses -> Mail).
        let field = app.secureTextFields["Password"]
        field.tap(); field.typeText(ProcessInfo.processInfo.environment["INBOX_DEV_PASSWORD"] ?? "dev-password-inbox-local")
        app.buttons["Sign in"].tap()
        openMail(app)
        XCTAssertTrue(app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Acme Studio'")).firstMatch.waitForExistence(timeout: 15),
                      "mail loads with the device token the login returned")
    }

    func testUnreachableServerShowsTheBannerNotAnError() throws {
        let app = launchSignedOut()
        XCTAssertTrue(app.buttons["Get started"].waitForExistence(timeout: 15))
        app.buttons["Get started"].tap()
        let address = app.textFields["Server address"]
        XCTAssertTrue(address.waitForExistence(timeout: 10))
        address.tap(); address.typeText("http://127.0.0.1:9")      // nothing listens on the discard port
        let field = app.secureTextFields["Password"]
        field.tap(); field.typeText("whatever")
        app.buttons["Sign in"].tap()
        XCTAssertTrue(element(app, containing: "Can't reach your server").waitForExistence(timeout: 20))
        XCTAssertTrue(app.buttons["Try again"].exists)
    }
}
