import Testing
import Foundation
import API
import Persistence
import Domain
@testable import FeatureGmail

private struct FakeAuth: WebAuthenticating {
    let callback: URL?
    @MainActor func authenticate(url: URL, callbackScheme: String) async throws -> URL {
        guard let callback else { throw CancellationError() }
        return callback
    }
}

private let statusJSON = #"{"state":"connected","email":"sam@gmail.com","connected_at":"2026-10-01T09:00:00Z","last_write_at":null,"waiting":14,"rate_limited_until":null,"configured":true}"#
private let connectJSON = #"{"auth_url":"https://accounts.google.com/x","callback_scheme":"inbox","expires_in":600}"#

@MainActor @Suite struct GmailTests {
    @Test func parsesCallbackURLs() {
        #expect(GmailConnectResult.parse(URL(string: "inbox://gmail/connected?status=ok")!) == .connected)
        #expect(GmailConnectResult.parse(URL(string: "inbox://gmail/connected?status=error&reason=access_denied")!) == .failed(reason: "access_denied"))
        #expect(GmailConnectResult.parse(URL(string: "inbox://message/123")!) == nil)
        #expect(GmailConnectResult.message(for: "access_denied")?.contains("didn't allow") == true)
    }

    @Test func statusDecodesAndSSEMerges() throws {
        let s = try APIClient.makeDecoder().decode(GmailStatus.self, from: Data(statusJSON.utf8))
        #expect(s.state == .connected && s.waiting == 14 && s.email == "sam@gmail.com")
        let store = GmailStatusStore(status: s)
        store.apply(["type": .string("gmail.status"), "state": .string("needs_reauth"), "email": .string("sam@gmail.com"), "waiting": .number(14)])
        #expect(store.needsReconnect && store.status?.waiting == 14)
        store.apply(["state": .string("connected"), "waiting": .number(5)])
        #expect(store.isCatchingUp && store.done == 9 && store.peakWaiting == 14)
        store.apply(["waiting": .number(0)])
        #expect(!store.isCatchingUp && store.peakWaiting == 0)
    }

    @Test func connectRunsBrowserThenRefreshes() async throws {
        let server = MockServer { req in
            switch req.path {
            case "/v1/gmail/connect": json(connectJSON)
            case "/v1/gmail/status": json(statusJSON)
            default: json("{}", status: 404)
            }
        }
        let env = AppEnvironment(api: server.client(), tokenStore: InMemoryTokenStore("t"), database: try .inMemory(), reachability: ManualReachability())
        let m = GmailModel(env: env, authenticator: FakeAuth(callback: URL(string: "inbox://gmail/connected?status=ok")!))
        await m.connect()
        #expect(env.gmail.isConnected && m.message == nil)
        #expect(server.requests.map(\.path) == ["/v1/gmail/connect", "/v1/gmail/status"])
    }

    @Test func failedCallbackShowsReasonAndCancelIsSilent() async throws {
        let server = MockServer { req in req.path == "/v1/gmail/connect" ? json(connectJSON) : json(statusJSON.replacingOccurrences(of: "connected\"", with: "not_connected\"")) }
        let env = AppEnvironment(api: server.client(), tokenStore: InMemoryTokenStore("t"), database: try .inMemory(), reachability: ManualReachability())
        let denied = GmailModel(env: env, authenticator: FakeAuth(callback: URL(string: "inbox://gmail/connected?status=error&reason=access_denied")!))
        await denied.connect()
        #expect(denied.message?.contains("didn't allow") == true)
        let cancelled = GmailModel(env: env, authenticator: FakeAuth(callback: nil))
        await cancelled.connect()
        #expect(cancelled.message == nil)
    }

    @Test func notConfiguredServerExplains() async throws {
        let server = MockServer { _ in json(#"{"type":"about:blank","title":"Service Unavailable","status":503,"code":"gmail_not_configured"}"#, status: 503,
                                            headers: ["Content-Type": "application/problem+json"]) }
        let env = AppEnvironment(api: server.client(), tokenStore: InMemoryTokenStore("t"), database: try .inMemory(), reachability: ManualReachability())
        let m = GmailModel(env: env, authenticator: FakeAuth(callback: nil))
        await m.connect()
        #expect(m.message == "Gmail isn't set up on your server yet.")
    }

    @Test func disconnectCallsServerAndRefreshes() async throws {
        let server = MockServer { req in
            req.path == "/v1/gmail:disconnect" ? (204, [:], Data()) : json(#"{"state":"not_connected","email":null,"connected_at":null,"last_write_at":null,"waiting":0,"rate_limited_until":null,"configured":true}"#)
        }
        let env = AppEnvironment(api: server.client(), tokenStore: InMemoryTokenStore("t"), database: try .inMemory(), reachability: ManualReachability())
        let m = GmailModel(env: env)
        await m.disconnect()
        #expect(env.gmail.status?.state == .notConnected)
    }

    @Test func importFiltersSplitsBlocked() async throws {
        let env = try AppEnvironment.preview()
        let m = ImportFiltersModel(env: env)
        await m.load()
        #expect(m.importable.count == 4 && m.blocked.count == 1 && m.selected.count == 4)
        await m.run()
        guard case .done(let n) = m.phase else { Issue.record("not done"); return }
        #expect(n == 4)
    }

    @Test func labelsProviderFallsBackWhenEndpointMissing() async throws {
        let server = MockServer { _ in json("{}", status: 404) }
        let p = APILabelsProvider(api: server.client())
        #expect(try await p.labels().contains("Receipts"))
    }
}
