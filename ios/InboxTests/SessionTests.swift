import Testing
import Foundation
@testable import API
import Persistence
import Domain
import FeatureSettings
@testable import FeatureOnboarding

private let unauthorized = #"{"type":"about:blank","title":"Unauthorized","status":401,"code":"unauthorized"}"#
private let problemHeader = ["Content-Type": "application/problem+json"]

/// Counts how often the client reports a rejected token.
private final class Reports: @unchecked Sendable {
    private let n = Mutex(0)
    var count: Int { n.withLock { $0 } }
    func bump() { n.withLock { $0 += 1 } }
}

@MainActor private var keep: [MockServer] = []

@MainActor
private func rig(token: String? = "old", _ handler: @escaping MockURLProtocol.Handler) throws -> (AppEnvironment, MockServer, InMemoryTokenStore) {
    let server = MockServer(handler); keep.append(server)
    let tokens = InMemoryTokenStore(token)
    let env = AppEnvironment(api: APIClient(baseURL: server.baseURL, tokenStore: tokens, session: server.session), tokenStore: tokens,
                             database: try .inMemory(), reachability: ManualReachability())
    return (env, server, tokens)
}

private func settle() async { try? await Task.sleep(for: .milliseconds(200)) }

@MainActor @Suite struct SessionLostTests {
    // MARK: 401 handling in the client

    @Test func a401ClearsTheTokenAndReportsOnce() async throws {
        let server = MockServer { _ in (401, problemHeader, Data(unauthorized.utf8)) }
        let tokens = InMemoryTokenStore("old")
        let api = APIClient(baseURL: server.baseURL, tokenStore: tokens, session: server.session)
        let reports = Reports()
        api.onUnauthorized { reports.bump() }
        await #expect(throws: APIError.self) { _ = try await api.businesses() }
        #expect(await tokens.token() == nil && reports.count == 1)
        #expect(server.baseURL == api.baseURL, "the saved server address is kept")
    }

    @Test func afterA401NothingMoreGoesToTheNetwork() async throws {
        let server = MockServer { _ in (401, problemHeader, Data(unauthorized.utf8)) }
        let api = APIClient(baseURL: server.baseURL, tokenStore: InMemoryTokenStore("old"), session: server.session)
        let reports = Reports()
        api.onUnauthorized { reports.bump() }
        _ = try? await api.businesses()
        for _ in 0..<25 {
            do { _ = try await api.businesses(); Issue.record("should fail") }
            catch let e as APIError { #expect(e == .notAuthenticated && e.isSessionLost && !e.isRetryable) }
        }
        #expect(server.requests.count == 1, "one request reached the server, the other 25 failed locally")
        #expect(reports.count == 1)
    }

    @Test func parallelRejectionsReportOnce() async throws {
        let server = MockServer { _ in (401, problemHeader, Data(unauthorized.utf8)) }
        let api = APIClient(baseURL: server.baseURL, tokenStore: InMemoryTokenStore("old"), session: server.session)
        let reports = Reports()
        api.onUnauthorized { reports.bump() }
        await withTaskGroup(of: Void.self) { g in for _ in 0..<8 { g.addTask { _ = try? await api.businesses() } } }
        #expect(reports.count == 1)
    }

    @Test func aNewerTokenIsNotClearedByAStaleRejection() async throws {
        let server = MockServer { _ in json("{}") }
        let tokens = InMemoryTokenStore("fresh")
        let api = APIClient(baseURL: server.baseURL, tokenStore: tokens, session: server.session)
        let reports = Reports()
        api.onUnauthorized { reports.bump() }
        await api.tokenRejected("stale")          // a request sent with the old token comes back 401 after a new sign-in
        #expect(await tokens.token() == "fresh" && reports.count == 0)
    }

    @Test func aWrongPasswordIsNotASessionLoss() async throws {
        let server = MockServer { _ in (401, problemHeader, Data(#"{"type":"about:blank","title":"x","status":401,"code":"invalid_credentials"}"#.utf8)) }
        let tokens = InMemoryTokenStore("keep")
        let api = APIClient(baseURL: server.baseURL, tokenStore: tokens, session: server.session)
        let reports = Reports()
        api.onUnauthorized { reports.bump() }
        await #expect(throws: LoginFailure.invalidCredentials) { try await api.login(server: server.baseURL, password: "x", deviceName: nil) }
        #expect(await tokens.token() == "keep" && reports.count == 0)
    }

    // MARK: Event stream

    @Test func eventStreamStopsOnA401AndDoesNotReconnect() async throws {
        let server = MockServer { _ in (401, problemHeader, Data(unauthorized.utf8)) }
        let api = APIClient(baseURL: server.baseURL, tokenStore: InMemoryTokenStore("old"), session: server.session)
        let reports = Reports()
        api.onUnauthorized { reports.bump() }
        var got: [SSEClient.Update] = []
        for await u in SSEClient(api: api, minBackoff: 0.01, maxBackoff: 0.02).events() { got.append(u) }
        #expect(got == [.unauthorized] && reports.count == 1)
        try await Task.sleep(for: .milliseconds(150))
        #expect(server.requests.count == 1, "no reconnect after the 401")
    }

    @Test func eventStreamWithoutATokenNeverTouchesTheNetwork() async throws {
        let server = MockServer { _ in json("{}") }
        let api = APIClient(baseURL: server.baseURL, tokenStore: InMemoryTokenStore(nil), session: server.session)
        var got: [SSEClient.Update] = []
        for await u in SSEClient(api: api, minBackoff: 0.01).events() { got.append(u) }
        #expect(got == [.unauthorized] && server.requests.isEmpty)
    }

    // MARK: App state

    @Test func environmentMarksTheSessionSignedOutOnce() async throws {
        let (env, server, tokens) = try rig { _ in (401, problemHeader, Data(unauthorized.utf8)) }
        #expect(!env.session.isSignedOut)
        _ = try? await env.api.businesses()
        await settle()
        let stored = await tokens.token()
        #expect(env.session.isSignedOut && env.session.rejections == 1 && stored == nil)
        for _ in 0..<10 { _ = try? await env.api.businesses(); try? await env.businesses.refresh(); _ = try? await env.mail.refresh() }
        await settle()
        #expect(env.session.rejections == 1 && server.requests.count == 1, "no retry storm: still one request, one rejection")
        env.session.signedIn()
        #expect(!env.session.isSignedOut && env.session.epoch == 1)
    }

    @Test func queuedChangesSurviveBeingSignedOut() async throws {
        let (env, _, _) = try rig { _ in (401, problemHeader, Data(unauthorized.utf8)) }
        let id = UUID()
        try await env.database.upsertMessages([MessageRecord(id: id, businessId: nil, receivedAt: .now)])
        try await env.database.enqueueOptimistic(kind: "archive", inverse: "unarchive", ids: [id])
        await env.outbox.flush()
        #expect(try await env.database.pendingOps().count == 1, "the archive stays queued for after the next sign-in")
        await env.outbox.flush()
        #expect(try await env.database.pendingOps().count == 1)
    }

    @Test func launchOpensSignedOutWhenSetupFinishedAndTheTokenIsGone() async throws {
        let (env, _, _) = try rig(token: nil) { _ in json("{}") }
        let d = scratchDefaults()
        #expect(await OnboardingState.hasLostSession(env: env, defaults: d) == false, "fresh install: Welcome")
        try await env.database.setPref(OnboardingState.key, OnboardingStep.done.rawValue)
        #expect(await OnboardingState.hasLostSession(env: env, defaults: d) == false, "no remembered server address: Welcome")
        d.set("https://mini.tail1234.ts.net", forKey: SignInModel.addressKey)
        #expect(await OnboardingState.hasLostSession(env: env, defaults: d))
        try await env.tokenStore.save("back")
        #expect(await OnboardingState.hasLostSession(env: env, defaults: d) == false)
    }

    @Test func signingInAgainStoresTheNewTokenAndKeepsTheAddress() async throws {
        let (env, server, tokens) = try rig(token: nil) { req in
            req.path == "/v1/auth/login" ? json(#"{"device_id":"00000000-0000-4000-8000-000000000001","token":"tok-2"}"#) : json("{}", status: 404)
        }
        env.session.markSignedOut()
        let d = scratchDefaults()
        let m = SignInModel(env: env, mode: .signedOut, defaults: d, deviceName: "Test")
        #expect(m.address == env.api.baseURL.absoluteString && m.initialFocus == .password)
        m.address = server.host      // https:// is added; the mock host isn't a local one
        m.password = "pw"
        #expect(await m.submit())
        let onboarding = OnboardingModel(env: env, defaults: d)
        onboarding.openSignIn(.signedOut)
        await onboarding.signedIn(.signedOut)
        env.session.signedIn()
        #expect(await tokens.token() == "tok-2" && !env.session.isSignedOut)
        #expect(d.string(forKey: SignInModel.addressKey) == "https://\(server.host)" && env.api.baseURL.host == server.host)
    }

    // MARK: setup_step (C-19)

    @Test func loginResponseDecodesSetupStepWhenPresentAndWhenAbsent() throws {
        let d = APIClient.makeDecoder()
        let with = try d.decode(LoginResponse.self, from: Data(#"{"device_id":"00000000-0000-4000-8000-000000000001","token":"t","setup_step":"gmail"}"#.utf8))
        let without = try d.decode(LoginResponse.self, from: Data(#"{"device_id":"00000000-0000-4000-8000-000000000001","token":"t"}"#.utf8))
        let null = try d.decode(LoginResponse.self, from: Data(#"{"device_id":"00000000-0000-4000-8000-000000000001","token":"t","setup_step":null}"#.utf8))
        #expect(with.setupStep == "gmail" && without.setupStep == nil && null.setupStep == nil)
    }

    private func resume(_ step: String?, businesses: Bool) async throws -> (OnboardingStep, Int) {
        let (env, server, _) = try rig(token: "t") { req in
            switch req.path {
            case "/v1/businesses": json(businesses ? #"{"businesses":[{"id":"\#(UUID().uuidString.lowercased())","name":"Acme","domain":"acme.com","sort_order":0,"hidden":false,"enabled":true,"routing":"ok","unread":0,"last_message_at":null,"addresses":[]}]}"# : #"{"businesses":[]}"#)
            case "/v1/cloudflare/domains": json(#"{"domains":[]}"#)
            default: json("{}", status: 404)
            }
        }
        let m = OnboardingModel(env: env, defaults: scratchDefaults())
        m.openSignIn(.existing)
        await m.signedIn(.existing, setupStep: step)
        #expect(m.signIn == nil)
        return (m.step, server.requests.filter { $0.path == "/v1/businesses" }.count)
    }

    @Test func existingResumesAtTheServersSetupStep() async throws {
        // The server's word wins over the business list, and costs no extra request.
        let done = try await resume("done", businesses: false)
        #expect(done.0 == .done && done.1 == 0)
        let gmail = try await resume("gmail", businesses: false)
        #expect(gmail.0 == .gmail && gmail.1 == 0)
        let domains = try await resume("domains", businesses: true)
        #expect(domains.0 == .domains && domains.1 == 0)
    }

    @Test func withoutSetupStepTheBusinessListDecides() async throws {
        let with = try await resume(nil, businesses: true)
        #expect(with.0 == .done && with.1 == 1)
        let empty = try await resume(nil, businesses: false)
        #expect(empty.0 == .domains)
        let unknown = try await resume("something-new", businesses: true)
        #expect(unknown.0 == .done, "an unknown value falls back like an absent one")
    }

    @Test func signInModelKeepsTheSetupStepFromTheLoginResponse() async throws {
        let (env, server, _) = try rig(token: nil) { _ in json(#"{"device_id":"00000000-0000-4000-8000-000000000001","token":"tok","setup_step":"gmail"}"#) }
        let m = SignInModel(env: env, mode: .existing, defaults: scratchDefaults(), deviceName: "T")
        m.address = server.host; m.password = "pw"
        let ok = await m.submit()
        #expect(ok && m.setupStep == "gmail")
    }

    // MARK: Open Runde licence (C-21)

    @Test func licenceTextIsTheFullOFLOrAHonestFallback() {
        let t = FontLicence.text()
        // The unit-test bundle is hosted in the app, so the bundled OFL.txt is found; without it the fallback names the licence.
        #expect(t.contains("SIL OPEN FONT LICENSE Version 1.1"), "OFL.txt is in the app bundle (tests are hosted in the app)")
        #expect(t.contains("PERMISSION AND CONDITIONS") && t.contains("DISCLAIMER"), "the whole text, not a header")
        #expect(FontLicence.text(bundle: Bundle(for: SessionBundleMarker.self)) == FontLicence.fallback, "a bundle without the file shows the fallback")
    }

    @Test func reflowKeepsEveryWordOfTheLicence() {
        let raw = FontLicence.text()
        let paras = FontLicence.paragraphs(raw)
        func words(_ s: String) -> [String] { s.split(whereSeparator: \.isWhitespace).map(String.init).filter { !$0.allSatisfy { $0 == "-" } } }
        #expect(words(paras.map(\.text).joined(separator: " ")) == words(raw), "same words in the same order, only the line breaks change")
        #expect(paras.count > 8 && paras.contains { $0.isHeading && $0.text == "PREAMBLE" } && paras.contains { $0.text.hasPrefix("1) ") })
        #expect(paras.allSatisfy { !$0.text.contains("\n") })
    }
}

private final class SessionBundleMarker {}
