import Testing
import Foundation
import API

/// Decodes responses captured from the real server (`make dev-server`, demo data) with the app's models, so a contract drift
/// between `api/openapi.yaml` and the Swift client shows up as a failing test. Refresh `Fixtures/live-samples.json` with
/// the capture snippet in docs/DEV_SERVER.md when the API changes.
@Suite struct LiveContractTests {
    private static let samples: [String: String] = {
        let url = URL(fileURLWithPath: #filePath).deletingLastPathComponent().appendingPathComponent("Fixtures/live-samples.json")
        let raw = (try? Data(contentsOf: url)).flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] } ?? [:]
        return raw.compactMapValues { $0 as? String }
    }()

    private func decode<T: Decodable>(_ key: String, as: T.Type = T.self) throws -> T {
        let s = try #require(Self.samples[key], "missing sample \(key)")
        do { return try APIClient.makeDecoder().decode(T.self, from: Data(s.utf8)) }
        catch { Issue.record("decoding \(key) as \(T.self): \(error)"); throw error }
    }

    @Test func messagesAndDetail() throws {
        let page: MessagePage = try decode("list")
        #expect(!page.messages.isEmpty && page.messages[0].route == "unrouted")
        let d: MessageDetail = try decode("detail")
        #expect(d.details.business?.name == "Acme Studio" && d.thread.count >= 1)
        let b: MessageBody = try decode("body")
        #expect(b.html?.contains("<html>") == true || b.text != nil)
    }

    @Test func businessesAndAttention() throws {
        let b: BusinessList = try decode("businesses")
        #expect(b.businesses.count == 3 && b.businesses.allSatisfy { !$0.domain.isEmpty })
        let a: AttentionSummary = try decode("attention")
        #expect(a.total == a.unrouted.count + a.failed.count)
        let u: UnroutedList = try decode("unrouted")
        #expect(u.total == a.unrouted.count && !u.groups.isEmpty)
    }

    @Test func rulesHealthPrefsBackfillAlfatherSearch() throws {
        let r: RuleList = try decode("rules"); #expect(r.rules.isEmpty)
        let h: HealthReport = try decode("health"); #expect(h.checks.contains { $0.id == "worker" })
        let p: Prefs = try decode("prefs"); #expect(p.previewLines == 2 && p.retentionDays == nil)
        let bf: [Backfill] = try decode("backfills", as: BackfillListShim.self).backfills; #expect(bf.isEmpty)
        let al: AlfatherStatus = try decode("alfather"); #expect(al.state() == .notPaired)
        let s: SearchResponse = try decode("search"); #expect(s.total >= 1)
        let rc: Recents = try decode("recents"); #expect(rc.recents.isEmpty)
    }
}

private struct BackfillListShim: Decodable { var backfills: [Backfill] }
