import Testing
import Foundation
import API
import Persistence
import Domain
@testable import FeatureGmail

@MainActor private var keepServers: [MockServer] = []

private func backfillJSON(id: UUID, biz: UUID, name: String, status: String, est: Int, scanned: Int, reason: String? = nil, until: String? = nil) -> String {
    #"{"id":"\#(id.uuidString.lowercased())","business_id":"\#(biz.uuidString.lowercased())","business_name":"\#(name)","domain":"x.co","since":"2026-07-13","status":"\#(status)","estimated":\#(est),"scanned":\#(scanned),"imported":\#(max(scanned - 2, 0)),"sorted":\#(scanned / 2),"unrouted":\#(scanned / 10),"skipped":2,"paused_until":\#(until.map { "\"\($0)\"" } ?? "null"),"pause_reason":\#(reason.map { "\"\($0)\"" } ?? "null"),"last_error":null,"created_at":"2026-10-11T09:00:00Z","started_at":"2026-10-11T09:00:05Z","finished_at":null}"#
}

@MainActor @Suite struct ImportOlderTests {
    @Test func storeAggregatesBatchAndMergesSSE() throws {
        let a = UUID(), b = UUID()
        let list = try [(a, "Acme", 1_000, 400), (b, "Kettle", 500, 100)].map { id, n, est, sc in
            try APIClient.makeDecoder().decode(Backfill.self, from: Data(backfillJSON(id: UUID(), biz: id, name: n, status: "running", est: est, scanned: sc).utf8))
        }
        let store = BackfillStore()
        store.set(list)
        #expect(store.isActive && store.isRunning && !store.isPaused)
        #expect(store.batchEstimated == 1_500 && store.batchScanned == 500)
        #expect(abs(store.progress - 500.0 / 1_500.0) < 0.0001)
        store.apply(["job": .string("backfill"), "backfill_id": .string(list[0].id.uuidString.lowercased()), "status": .string("paused"),
                     "scanned": .number(450), "paused_until": .string("2026-10-11T09:30:00Z")])
        #expect(store.backfills[0].status == .paused && store.backfills[0].scanned == 450 && store.pausedUntil != nil)
        store.apply(["job": .string("backfill"), "backfill_id": .string(list[0].id.uuidString.lowercased()), "status": .string("done"), "scanned": .number(1_000)])
        store.apply(["job": .string("backfill"), "backfill_id": .string(list[1].id.uuidString.lowercased()), "status": .string("done"), "scanned": .number(500)])
        #expect(store.batchFinished && !store.isActive)
    }

    @Test func estimateAndStartUseTheChosenRange() async throws {
        let biz = UUID()
        let server = MockServer { req in
            switch (req.method, req.path) {
            case ("GET", "/v1/backfills:estimate"): return json(#"{"since":"2026-07-13","total":3200,"eta_seconds":320,"businesses":[]}"#)
            case ("POST", "/v1/backfills"): return json(#"{"backfills":[\#(backfillJSON(id: UUID(), biz: biz, name: "Acme", status: "queued", est: 3200, scanned: 0))]}"#, status: 201)
            case ("GET", "/v1/backfills"): return json(#"{"backfills":[\#(backfillJSON(id: UUID(), biz: biz, name: "Acme", status: "running", est: 3200, scanned: 100))]}"#)
            default: return json("{}", status: 404)
            }
        }
        keepServers.append(server)
        let env = AppEnvironment(api: server.client(), tokenStore: InMemoryTokenStore("t"), database: try .inMemory(), reachability: ManualReachability())
        let m = ImportOlderModel(env: env)
        await m.refreshEstimate()
        #expect(m.estimate?.total == 3_200 && m.etaText == "about 5 min")
        #expect(server.requests.last?.url.query?.contains("days=90") == true)
        await m.start()
        let post = server.requests.last { $0.method == "POST" }
        #expect(post?.json?["days"] as? Int == 90)
        #expect(env.backfill.isRunning && env.backfill.days == 90)
        #expect(m.rangeTitle == "last 90 days" && m.businessSummary == "All 0 businesses")
    }

    @Test func errorsExplainThemselves() async throws {
        let server = MockServer { _ in json(#"{"type":"about:blank","title":"Conflict","status":409,"code":"gmail_not_connected"}"#, status: 409, headers: ["Content-Type": "application/problem+json"]) }
        keepServers.append(server)
        let env = AppEnvironment(api: server.client(), tokenStore: InMemoryTokenStore("t"), database: try .inMemory(), reachability: ManualReachability())
        let m = ImportOlderModel(env: env)
        await m.refreshEstimate()
        #expect(m.message == "Connect Gmail first, in Settings → Gmail." && m.estimate == nil)
        await m.start()
        #expect(m.message == "Connect Gmail first, in Settings → Gmail.")
    }

    @Test func pauseTextCoversEachReason() throws {
        let env = try AppEnvironment.preview()
        let m = ImportOlderModel(env: env)
        let b = try APIClient.makeDecoder().decode(Backfill.self, from: Data(backfillJSON(id: UUID(), biz: UUID(), name: "A", status: "paused", est: 10, scanned: 5, reason: "rate_limited", until: "2026-10-11T09:30:00Z").utf8))
        env.backfill.set([b])
        #expect(m.pauseText().title == "Paused by Gmail rate limits.")
        var s = b; s.pauseReason = "gmail_signed_out"; env.backfill.set([s])
        #expect(m.pauseText().title == "Paused: Gmail signed out")
        var u = b; u.pauseReason = "user"; env.backfill.set([u])
        #expect(m.pauseText().title == "Import paused")
    }
}
