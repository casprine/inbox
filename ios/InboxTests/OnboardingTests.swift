import Testing
import Foundation
import API
import Persistence
import Domain
import FeatureGmail
import UIKit
@testable import FeatureOnboarding

private struct NoAlfather: AlfatherDiscovering {
    func discover(timeout: Duration) async -> AlfatherService? { nil }
    func deliver(_ grant: AlfatherPairingGrant, to service: AlfatherService) async throws {}
}

private let authJSON = #"{"device_id":"00000000-0000-4000-8000-000000000001","token":"tok-1"}"#
private let domainsJSON = #"{"domains":[{"zone_id":"z1","domain":"acme-studio.com","status":"active","routing":"ok","already_added":false,"selectable":true},{"zone_id":"z3","domain":"kettle.house","status":"active","routing":"to_email","already_added":false,"selectable":false}]}"#
private func businessJSON(_ name: String, _ domain: String) -> String {
    #"{"id":"\#(UUID().uuidString.lowercased())","name":"\#(name)","domain":"\#(domain)","sort_order":0,"hidden":false,"enabled":true,"routing":"ok","unread":0,"last_message_at":null,"addresses":[]}"#
}

/// A throwaway UserDefaults suite so sign-in tests never touch the app's remembered server address.
func scratchDefaults() -> UserDefaults { UserDefaults(suiteName: "inbox.tests.\(UUID().uuidString)")! }

private let problemHeaders = ["Content-Type": "application/problem+json"]
private func problem(_ status: Int, _ code: String, headers: [String: String] = [:]) -> (status: Int, headers: [String: String], body: Data) {
    json(#"{"type":"about:blank","title":"x","status":\#(status),"code":"\#(code)"}"#, status: status, headers: problemHeaders.merging(headers) { $1 })
}

/// Servers stay registered for the whole run (a released MockServer unregisters its handler).
@MainActor private var keepAlive: [MockServer] = []

@MainActor
private func rig(token: String? = "t", handler: @escaping MockURLProtocol.Handler) throws -> (OnboardingModel, AppEnvironment, MockServer, InMemoryTokenStore) {
    let server = MockServer(handler)
    keepAlive.append(server)
    let tokens = InMemoryTokenStore(token)
    let env = AppEnvironment(api: APIClient(baseURL: server.baseURL, tokenStore: tokens, session: server.session), tokenStore: tokens, database: try .inMemory(), reachability: ManualReachability())
    let m = OnboardingModel(env: env, authenticator: MockWebAuthenticator(), defaults: scratchDefaults(), estimator: StubBackfillEstimator(), discovery: NoAlfather())
    return (m, env, server, tokens)
}

@MainActor @Suite struct OnboardingTests {
    @Test func businessNamesFromDomains() {
        #expect(OnboardingModel.name(forDomain: "acme-studio.com") == "Acme Studio")
        #expect(OnboardingModel.name(forDomain: "kettle.house") == "Kettle")
        #expect(OnboardingModel.name(forDomain: "casprine.dev") == "Casprine")
    }

    // MARK: Sign in (OB01b)

    @Test func welcomeButtonsOnlyOpenTheSignInScreen() async throws {
        let (m, _, server, _) = try rig { _ in json("{}", status: 500) }
        m.openSignIn(.new)
        #expect(m.step == .welcome && m.signIn?.mode == .new && m.signIn?.address == "")
        m.back()
        #expect(m.signIn == nil && m.step == .welcome)
        m.openSignIn(.existing)
        #expect(m.signIn?.mode == .existing)
        #expect(server.requests.isEmpty, "opening the screen sends nothing")
    }

    @Test func loginStoresTokenKeepsAddressDropsPasswordAndMovesToDomains() async throws {
        let (m, env, server, tokens) = try rig(token: nil) { req in
            switch req.path {
            case "/v1/auth/login": json(authJSON)
            case "/v1/cloudflare/domains": json(domainsJSON)
            default: json("{}", status: 404)
            }
        }
        m.openSignIn(.new)
        let s = try #require(m.signIn)
        s.address = "  HTTPS://\(server.host.uppercased())/some/path/ "
        s.password = "correct horse"
        #expect(s.canSubmit)
        #expect(await s.submit())
        #expect(s.address == "https://\(server.host)", "the normalised address is shown back")
        #expect(s.password == "" && s.isLoading, "password dropped; stays loading until the next screen is up")
        await m.signedIn(.new)
        #expect(m.step == .domains && m.signIn == nil && m.banner == nil)
        #expect(await tokens.token() == "tok-1")
        #expect(m.domains.count == 2 && m.selected == ["z1"])
        #expect(await OnboardingState.step(env.database) == .domains)
        let login = try #require(server.requests.first)
        #expect(login.method == "POST" && login.path == "/v1/auth/login" && login.headers["Authorization"] == nil)
        #expect(login.json?["password"] as? String == "correct horse" && (login.json?["device_name"] as? String)?.isEmpty == false)
        #expect(env.api.baseURL.host == server.host)
    }

    @Test func rememberedAddressPrefillsExistingAndSignedOutModes() async throws {
        let (_, env, server, _) = try rig { _ in json(authJSON) }
        let d = scratchDefaults()
        let a = SignInModel(env: env, mode: .existing, defaults: d)
        #expect(a.address == "" && a.initialFocus == .address)
        a.address = "https://\(server.host)"; a.password = "pw"
        #expect(await a.submit())
        #expect(d.string(forKey: SignInModel.addressKey) == "https://\(server.host)")
        let b = SignInModel(env: env, mode: .existing, defaults: d)
        #expect(b.address == "https://\(server.host)" && b.initialFocus == .password)
        let c = SignInModel(env: env, mode: .signedOut, defaults: scratchDefaults())
        #expect(c.address == env.api.baseURL.absoluteString && c.initialFocus == .password && c.password == "")
        #expect(SignInCopy.title(.signedOut) == "Sign in again" && SignInCopy.title(.existing) == "Welcome back")
    }

    @Test func existingGoesToMailWhenTheServerHasBusinesses() async throws {
        let (m, env, _, _) = try rig { req in
            req.path == "/v1/businesses" ? json(#"{"businesses":[\#(businessJSON("Acme Studio", "acme-studio.com"))]}"#) : json("{}", status: 404)
        }
        m.openSignIn(.existing)
        await m.signedIn(.existing)
        #expect(m.step == .done && m.signIn == nil)
        #expect(await OnboardingState.isComplete(env.database))
        // An empty server resumes at OB02.
        let (n, _, _, _) = try rig { req in
            req.path == "/v1/businesses" ? json(#"{"businesses":[]}"#) : req.path == "/v1/cloudflare/domains" ? json(domainsJSON) : json("{}", status: 404)
        }
        n.openSignIn(.existing)
        await n.signedIn(.existing)
        #expect(n.step == .domains)
    }

    @Test func wrongPasswordClearsThePasswordAndKeepsTheAddress() async throws {
        let (m, _, server, tokens) = try rig(token: nil) { _ in problem(401, "invalid_credentials") }
        m.openSignIn(.new)
        let s = try #require(m.signIn)
        s.address = server.host; s.password = "nope"; s.revealed = true
        #expect(await s.submit() == false)
        #expect(s.issue == .wrongPassword && s.issue?.inlineText == "That password isn't right.")
        #expect(s.password == "" && s.revealed == false && s.address == "https://\(server.host)" && !s.isLoading)
        #expect(s.focusRequest?.field == .password)
        #expect(await tokens.token() == nil)
        s.password = "x"
        #expect(s.issue == nil, "editing the errored field clears the error")
    }

    @Test func lockOutCountsDownDisablesSigningInAndSurvivesANewScreen() async throws {
        let clock = Mutex<Date>(Date(timeIntervalSince1970: 1_000_000))
        let (_, env, server, _) = try rig(token: nil) { _ in problem(429, "rate_limited", headers: ["Retry-After": "272"]) }
        let make = { SignInModel(env: env, mode: .new, defaults: scratchDefaults(), deviceName: "Test", now: { clock.withLock { $0 } }) }
        let s = make()
        s.address = server.host; s.password = "pw"
        #expect(await s.submit() == false)
        #expect(s.lockRemaining() == 272 && s.issue == nil && !s.canSubmit)
        #expect(SignInCopy.lockButton(remaining: 272) == "Try again in 4:32")
        #expect(SignInCopy.lockMessage(remaining: 272) == "Sign-in is paused to protect your server. Try again in 4:32.")
        #expect(SignInCopy.spoken(272) == "4 minutes 32 seconds")
        // Leaving and coming back in the same launch: still locked.
        let again = make()
        again.address = "https://\(server.host)"; again.password = "pw"
        #expect(again.lockRemaining() == 272 && !again.canSubmit)
        clock.withLock { $0 = $0.addingTimeInterval(100) }
        #expect(s.lockRemaining() == 172)
        clock.withLock { $0 = $0.addingTimeInterval(172) }
        #expect(s.lockRemaining() == nil && s.canSubmit, "at 0 the button is back")
    }

    @Test func lockTextRoundsAtTenMinutesAndAnHour() {
        #expect(SignInCopy.lockMessage(remaining: 599).hasSuffix("Try again in 9:59."))
        #expect(SignInCopy.lockMessage(remaining: 600).hasSuffix("Try again in about 10 minutes."))
        #expect(SignInCopy.lockMessage(remaining: 841).hasSuffix("Try again in about 15 minutes."))
        #expect(SignInCopy.lockMessage(remaining: 900).hasSuffix("Try again in about 15 minutes."))
        #expect(SignInCopy.lockMessage(remaining: 3_600).hasSuffix("Try again in about 1 hour."))
        #expect(SignInCopy.lockButton(remaining: 900) == "Try again later" && SignInCopy.lockButton(remaining: 5) == "Try again in 0:05")
    }

    @Test func serverAndNetworkFailuresShowTheRightSurface() async throws {
        func attempt(_ h: @escaping MockURLProtocol.Handler) async throws -> SignInModel.Issue? {
            let (m, _, server, _) = try rig(token: nil, handler: h)
            m.openSignIn(.new)
            let s = try #require(m.signIn)
            s.address = server.host; s.password = "pw"
            _ = await s.submit()
            #expect(!s.isLoading && s.password == "pw", "form is usable again and keeps what was typed")
            return s.issue
        }
        #expect(try await attempt { _ in problem(500, "internal") } == .unreachable(.server))
        #expect(try await attempt { _ in json("{}", status: 503) } == .unreachable(.server))
        #expect(try await attempt { _ in throw URLError(.cannotConnectToHost) } == .unreachable(.network))
        #expect(try await attempt { _ in throw URLError(.timedOut) } == .unreachable(.network))
        let tls = try await attempt { _ in throw URLError(.serverCertificateUntrusted) }
        guard case .unreachable(.tls(let host))? = tls else { Issue.record("expected tls, got \(String(describing: tls))"); return }
        #expect(SignInModel.bannerText(.tls(host: host)).message == "Couldn't make a secure connection to \(host).")
        #expect(SignInModel.bannerText(.server).message == "Your server answered with an error. Try again in a moment.")
        #expect(try await attempt { _ in json(#"{"hello":"world"}"#) } == .notInboxServer)
        #expect(try await attempt { _ in json("<html>nope</html>", status: 404) } == .notInboxServer)
        #expect(SignInModel.Issue.notInboxServer.inlineText == "That address doesn't look like an Inbox server.")
    }

    @Test func invalidAddressesSendNothing() async throws {
        let (m, _, server, _) = try rig(token: nil) { _ in json(authJSON) }
        m.openSignIn(.new)
        let s = try #require(m.signIn)
        s.password = "pw"
        for bad in ["not an address", "inbox", "ftp://inbox.example.com", "https://"] {
            s.address = bad
            #expect(await s.submit() == false)
            #expect(s.issue == .invalidURL(insecure: false), "\(bad)")
            #expect(s.issue?.inlineText == "That doesn't look like a server address. Try something like inbox.example.com.")
            #expect(s.focusRequest?.field == .address)
        }
        s.address = "http://inbox.example.com"
        #expect(await s.submit() == false)
        #expect(s.issue == .invalidURL(insecure: true) && s.issue?.inlineText == "Use https:// for a server on the internet.")
        #expect(s.password == "pw")
        #expect(server.requests.isEmpty)
    }

    @Test func buttonNeedsBothFields() throws {
        let (m, _, _, _) = try rig { _ in json("{}") }
        m.openSignIn(.new)
        let s = try #require(m.signIn)
        #expect(!s.canSubmit)
        s.address = "  "; s.password = "pw"
        #expect(!s.canSubmit, "address is trimmed")
        s.address = "inbox.example.com"; s.password = ""
        #expect(!s.canSubmit)
        s.password = " "
        #expect(s.canSubmit, "the password is not trimmed")
    }

    @Test func serverAddressNormalisation() {
        func ok(_ s: String) -> String? { if case .success(let u) = ServerAddress.normalise(s) { u.absoluteString } else { nil } }
        func why(_ s: String) -> ServerAddress.Invalid? { if case .failure(let e) = ServerAddress.normalise(s) { e } else { nil } }
        #expect(ok("inbox.example.com") == "https://inbox.example.com")
        #expect(ok("  Inbox.Example.COM/  ") == "https://inbox.example.com")
        #expect(ok("https://mini.tail1234.ts.net/v1/foo?x=1#y") == "https://mini.tail1234.ts.net")
        #expect(ok("mini.tail1234.ts.net:8443/") == "https://mini.tail1234.ts.net:8443")
        #expect(ok("HTTP://localhost:8080") == "http://localhost:8080")
        #expect(ok("http://127.0.0.1:18090/") == "http://127.0.0.1:18090")
        #expect(ok("http://192.168.1.20:8080") == "http://192.168.1.20:8080")
        #expect(ok("http://172.20.0.5") == "http://172.20.0.5" && ok("http://10.0.0.2") == "http://10.0.0.2")
        #expect(ok("http://mac-mini.local") == "http://mac-mini.local" && ok("http://dev.localhost") == "http://dev.localhost")
        #expect(ok("localhost:8080") == "https://localhost:8080")
        #expect(why("http://inbox.example.com") == .insecure && why("http://172.32.0.1") == .insecure && why("http://8.8.8.8") == .insecure)
        #expect(why("") == .malformed && why("   ") == .malformed && why("two words.com") == .malformed)
        #expect(why("inbox") == .malformed && why("ftp://inbox.example.com") == .malformed && why("https://") == .malformed && why("mailto:a@b.co") == .malformed)
    }

    @Test func noAppleSignInRemains() {
        // Guard against a regression: the welcome screen copy no longer mentions Apple.
        let text = [SignInCopy.title(.new), SignInCopy.caption(.new), SignInCopy.caption(.existing), SignInCopy.caption(.signedOut)].joined()
        #expect(!text.localizedCaseInsensitiveContains("apple"))
    }

    @Test func fixRoutingThenCheckAgainTurnsSwitchOn() async throws {
        let (m, _, srv_m, _) = try rig { req in
            if req.path == "/v1/cloudflare/domains" { return json(domainsJSON) }
            if req.path == "/v1/cloudflare/domains/z3:check" {
                return json(#"{"zone_id":"z3","domain":"kettle.house","status":"active","routing":"ok","already_added":false,"selectable":true}"#)
            }
            return json("{}", status: 404)
        }
        await m.loadDomains()
        let kettle = m.domains.first { $0.id == "z3" }!
        m.toggle(kettle)                       // not selectable: ignored
        #expect(!m.selected.contains("z3"))
        m.fixing = kettle
        await m.checkAgain(kettle)
        #expect(m.selected.contains("z3") && m.fixing == nil)
    }

    @Test func continueCreatesBusinessesAndAdvances() async throws {
        let (m, env, server, _) = try rig { req in
            switch req.path {
            case "/v1/cloudflare/domains": json(domainsJSON)
            case "/v1/businesses" where req.method == "POST": json(businessJSON("Acme Studio", "acme-studio.com"), status: 201)
            case "/v1/businesses": json(#"{"businesses":[]}"#)
            default: json("{}", status: 404)
            }
        }
        await m.loadDomains()
        await m.continueWithDomains()
        let posts = server.requests.filter { $0.path == "/v1/businesses" && $0.method == "POST" }
        #expect(posts.count == 1 && posts[0].json?["name"] as? String == "Acme Studio" && posts[0].json?["cf_zone_id"] as? String == "z1")
        #expect(m.step == .gmail)
        #expect(await OnboardingState.step(env.database) == .gmail)
    }

    @Test func stepsAdvanceAndSkipPaths() async throws {
        let (m, env, srv_m, _) = try rig { req in
            req.path == "/v1/gmail/connect" ? json(#"{"auth_url":"https://accounts.google.com/x","callback_scheme":"inbox","expires_in":600}"#)
            : req.path == "/v1/gmail/status" ? json(#"{"state":"connected","email":"sam@gmail.com","connected_at":null,"last_write_at":null,"waiting":0,"rate_limited_until":null,"configured":true}"#)
            : json("{}", status: 404)
        }
        m.jump(to: .gmail)
        await m.connectGmail()
        #expect(m.step == .gmailConnected)               // OB07
        #expect(OnboardingStep.gmailConnected.number == 3)
        await m.continueFromGmailConnected()
        #expect(m.step == .filters)
        await m.finishFilters(imported: 3)
        #expect(m.step == .backfill)
        await m.loadEstimates()
        #expect(m.estimates[90] == 3_200)
        await m.finishBackfill(start: true)
        #expect(m.step == .alfather)
        #expect(try await env.database.pref("onboarding.backfill_days") == "90")
        await m.finishAlfather(pair: false)
        #expect(m.step == .ready)                        // OB12: not complete until "Open Inbox"
        #expect(await OnboardingState.isComplete(env.database) == false)
        await m.openInbox()
        #expect(m.step == .done)
        #expect(await OnboardingState.isComplete(env.database))
        // Skipping Gmail skips filters and import too: straight to Alfather.
        let (n, _, srv_n, _) = try rig { _ in json("{}") }
        n.jump(to: .gmail); await n.skipGmail()
        #expect(n.step == .alfather)
    }

    @Test func readySummaryNamesWhatApplies() {
        #expect(OnboardingModel.summary(businesses: 4, rules: 3, backfillDays: 90) == "4 businesses, 3 rules from Gmail, and 90 days of mail on its way.")
        #expect(OnboardingModel.summary(businesses: 1, rules: 1, backfillDays: 365) == "1 business, 1 rule from Gmail, and a year of mail on its way.")
        #expect(OnboardingModel.summary(businesses: 2, rules: 0, backfillDays: 30) == "2 businesses and 30 days of mail on its way.")
        #expect(OnboardingModel.summary(businesses: 3, rules: 0, backfillDays: 0) == "3 businesses.")
        #expect(OnboardingModel.summary(businesses: 0, rules: 0, backfillDays: 0) == "New mail will start to arrive here.")
    }

    @Test func readyStepResumesAndOnlyOpenInboxCompletesSetup() async throws {
        let (m, env, _, _) = try rig(token: "t") { _ in json("{}") }
        try await env.database.setPref(OnboardingState.key, OnboardingStep.ready.rawValue)
        #expect(await OnboardingState.needsOnboarding(env: env, devToken: nil) == true)
        await m.resume()
        #expect(m.step == .ready)
        try await env.database.setPref("onboarding.backfill_days", "90")
        await m.loadSummary()
        #expect(m.summary.hasSuffix("90 days of mail on its way."))
        await m.openInbox()
        #expect(await OnboardingState.needsOnboarding(env: env, devToken: nil) == false)
    }

    @Test func gmailConnectedStepIsBackToConnectAndForwardToFilters() async throws {
        let (m, _, _, _) = try rig { _ in json("{}") }
        m.jump(to: .gmailConnected)
        m.back()
        #expect(m.step == .gmail)
        #expect(OnboardingStep.filters.previous == .gmailConnected)
    }

    @Test func launchGateGoesStraightToMailWhenATokenIsSeededOrSessionExists() async throws {
        // Fresh install + seeded dev token: no onboarding, token stored, step done (regression: it used to show onboarding once).
        let (_, env, _, tokens) = try rig(token: nil) { _ in json("{}") }
        #expect(await OnboardingState.needsOnboarding(env: env, devToken: "dev-token-inbox-local-0000") == false)
        let seeded = await tokens.token()
        let done = await OnboardingState.isComplete(env.database)
        #expect(seeded == "dev-token-inbox-local-0000" && done)
        // Fresh install, nothing seeded: onboarding.
        let (_, env2, _, _) = try rig(token: nil) { _ in json("{}") }
        let a = await OnboardingState.needsOnboarding(env: env2, devToken: nil)
        let b = await OnboardingState.needsOnboarding(env: env2, devToken: "")
        #expect(a == true && b == true)
        // Valid session (token + step done): Mail. Token but unfinished steps: onboarding.
        let (_, env3, _, _) = try rig(token: "t") { _ in json("{}") }
        try await env3.database.setPref(OnboardingState.key, OnboardingStep.done.rawValue)
        let session = await OnboardingState.needsOnboarding(env: env3, devToken: nil)
        #expect(session == false)
        try await env3.database.setPref(OnboardingState.key, OnboardingStep.gmail.rawValue)
        let unfinished = await OnboardingState.needsOnboarding(env: env3, devToken: nil)
        #expect(unfinished == true)
    }

    @Test func resumeNeedsTokenAndBackReturns() async throws {
        let (m, env, srv_m, tokens) = try rig(token: nil) { _ in json("{}") }
        try await env.database.setPref(OnboardingState.key, OnboardingStep.backfill.rawValue)
        await m.resume()
        #expect(m.step == .welcome)             // no device token: start over
        try await tokens.save("t")
        await m.resume()
        #expect(m.step == .backfill)
        m.back()
        #expect(m.step == .filters)
        #expect(OnboardingStep.alfather.number == 6 && OnboardingStep.total == 7)
    }
}
