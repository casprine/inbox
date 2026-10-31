import Testing
import Foundation
import API
import Persistence
import Domain
@testable import FeatureSettings

@MainActor private var keepPrivacy: [MockServer] = []

@MainActor
private func rig(_ handler: @escaping MockURLProtocol.Handler) throws -> (AppEnvironment, MockServer) {
    let server = MockServer(handler)
    keepPrivacy.append(server)
    let env = AppEnvironment(api: server.client(), tokenStore: InMemoryTokenStore("t"), database: try .inMemory(), reachability: ManualReachability())
    return (env, server)
}

@MainActor @Suite struct PrivacyTests {
    @Test func prefsPatchEncodesOnlySetFieldsAndExplicitNull() throws {
        let enc = APIClient.makeEncoder()
        func obj(_ p: PrefsPatch) throws -> [String: Any] { try JSONSerialization.jsonObject(with: enc.encode(p)) as! [String: Any] }
        #expect(try obj(PrefsPatch(retention: .days(90))) as NSDictionary == ["retention_days": 90])
        let forever = try obj(PrefsPatch(retention: .keepForever))
        #expect(forever.count == 1 && forever["retention_days"] is NSNull)
        #expect(try obj(PrefsPatch(imageAllowSenders: ["a@b.co"], previewLines: 3)) as NSDictionary == ["image_allow_senders": ["a@b.co"], "preview_lines": 3])
        #expect(try obj(PrefsPatch()).isEmpty)
    }

    @Test func retentionChangeSavesAndRollsBackOnFailure() async throws {
        let (env, server) = try rig { req in
            req.method == "PUT" ? json(#"{"retention_days":90,"image_allow_senders":[],"preview_lines":2}"#) : json(#"{"retention_days":365,"image_allow_senders":[],"preview_lines":2}"#)
        }
        let m = PrivacyModel(env: env)
        await m.load()
        #expect(m.retentionDays == 365 && m.retentionTitle == "1 year")
        await m.setRetention(90)
        #expect(m.retentionDays == 90 && server.requests.last?.json?["retention_days"] as? Int == 90)
        await m.setRetention(nil)
        #expect(m.retentionTitle == "Forever" && server.requests.last?.json?["retention_days"] is NSNull)
        let (bad, _) = try rig { _ in json("{}", status: 500) }
        let n = PrivacyModel(env: bad)
        await n.setRetention(30)
        #expect(n.retentionDays == 365 && n.notice != nil)
    }

    @Test func deleteEverythingNeedsTheTypedCodeThenClearsLocalMail() async throws {
        let (env, server) = try rig { req in
            switch (req.method, req.path) {
            case ("POST", "/v1/data:confirm"): json(#"{"token":"DELETE-4F7K2M9Q","expires_at":"2026-10-11T10:00:00Z","expires_in":300}"#)
            case ("DELETE", "/v1/data"): json(#"{"deleted_messages":120,"deleted_blobs":240}"#)
            default: json("{}", status: 404)
            }
        }
        try await MockData.seed(env.database)
        #expect(try await env.database.messages().isEmpty == false)
        let m = PrivacyModel(env: env)
        await m.beginDelete()
        #expect(m.token == "DELETE-4F7K2M9Q" && !m.canDelete)
        m.typed = "delete-4f7k2m9x"
        #expect(!m.canDelete)
        m.typed = "delete-4f7k2m9q"           // case-insensitive
        #expect(m.canDelete)
        await m.confirmDelete()
        #expect(m.deleted?.deletedMessages == 120 && m.token == nil)
        #expect(server.requests.last?.json?["confirm"] as? String == "DELETE-4F7K2M9Q")
        #expect(try await env.database.messages().isEmpty)
        #expect(try await env.database.businesses().count == 3)        // businesses stay
    }

    @Test func expiredCodeAsksToStartAgain() async throws {
        let (env, _) = try rig { req in
            req.path == "/v1/data:confirm" ? json(#"{"token":"DELETE-AAAA","expires_in":300}"#)
            : json(#"{"type":"about:blank","title":"Forbidden","status":403,"code":"confirmation_invalid"}"#, status: 403, headers: ["Content-Type": "application/problem+json"])
        }
        let m = PrivacyModel(env: env)
        await m.beginDelete(); m.typed = "DELETE-AAAA"
        await m.confirmDelete()
        #expect(m.deleteError?.contains("expired") == true && m.token == nil && m.deleted == nil)
    }

    @Test func pullMergesAllowListBothWays() async throws {
        let (env, server) = try rig { req in
            req.method == "GET" ? json(#"{"retention_days":90,"image_allow_senders":["@acme.com"],"preview_lines":3}"#) : json(#"{"retention_days":90,"image_allow_senders":[],"preview_lines":3}"#)
        }
        try await env.database.setPref(PrefsSync.allowKey, #"["news@shop.com"]"#)
        await PrefsSync.pull(env)
        #expect(Set(await PrefsSync.localSenders(env.database)) == ["news@shop.com", "@acme.com"])
        let put = server.requests.first { $0.method == "PUT" && ($0.json?["image_allow_senders"] != nil) }
        #expect((put?.json?["image_allow_senders"] as? [String])?.sorted() == ["@acme.com", "news@shop.com"])
        #expect(env.theme.previewLines == 3)
        #expect(try await env.database.pref(PrefsSync.retentionKey) == "90")
    }
}
