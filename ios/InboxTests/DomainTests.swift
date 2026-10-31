import Testing
import Foundation
import API
import Persistence
import Domain

/// Manually driven connectivity.
struct ManualReachability: Reachability {
    let stream: AsyncStream<Bool>, cont: AsyncStream<Bool>.Continuation
    init() { (stream, cont) = AsyncStream.makeStream() }
    func updates() -> AsyncStream<Bool> { stream }
}

private struct Rig {
    let server: MockServer, db: AppDatabase, api: APIClient, outbox: OutboxWorker, mail: MailRepository, businesses: BusinessRepository
    init(_ handler: @escaping MockURLProtocol.Handler) throws {
        server = MockServer(handler)
        db = try .inMemory()
        api = server.client()
        outbox = OutboxWorker(api: api, db: db, reachability: ManualReachability(), kickDelay: .seconds(300))
        mail = MailRepository(api: api, db: db, outbox: outbox)
        businesses = BusinessRepository(api: api, db: db)
    }
    func batchRequests() -> [CapturedRequest] { server.requests.filter { $0.path == "/v1/messages:batch" } }
}

private func echoBatch(_ req: CapturedRequest) -> (status: Int, headers: [String: String], body: Data) {
    let ids = (req.json?["ids"] as? [String]) ?? []
    let op = req.json?["op"] as? String ?? ""
    let msgs = ids.map { messageJSON(id: UUID(uuidString: $0)!, read: op == "read", archived: op == "archive") }
    return json(#"{"messages":[\#(msgs.joined(separator: ","))]}"#)
}

@Suite struct PersistenceTests {
    @Test func migrationsCreateAllTables() async throws {
        let db = try AppDatabase.inMemory()
        let names = try await db.writer.read { try String.fetchAll($0, sql: "SELECT name FROM sqlite_master WHERE type='table'") }
        for t in ["business", "message", "message_body", "thread_item", "pref", "recent_search", "pending_op"] { #expect(names.contains(t)) }
        try await db.setPref("k", "v")
        #expect(try await db.pref("k") == "v")
        try await db.replaceRecentSearches(["b", "a"])
        #expect(try await db.recentSearches() == ["b", "a"])
    }
}

@Suite struct RepositoryTests {
    @Test func refreshUpsertsAndObserverSeesIt() async throws {
        let a = UUID(), b = UUID()
        let rig = try Rig { _ in json(#"{"messages":[\#(messageJSON(id: a, subject: "one")),\#(messageJSON(id: b, subject: "two", at: "2026-10-02T10:00:00Z"))],"next_cursor":"n"}"#) }
        var it = rig.mail.observe().makeAsyncIterator()
        #expect(await it.next() == [])            // cold cache renders immediately
        let cursor = try await rig.mail.refresh()
        #expect(cursor == "n")
        var seen: [MessageRecord] = []
        while let rows = await it.next() { if !rows.isEmpty { seen = rows; break } }
        #expect(seen.map(\.subject) == ["two", "one"])   // newest first
        // Second refresh with changed server state updates in place.
        let rig2 = try Rig { _ in json(#"{"messages":[\#(messageJSON(id: a, read: true, subject: "one!"))],"next_cursor":null}"#) }
        try await rig2.db.upsertMessages(seen)
        try await rig2.mail.refresh()
        let row = try await rig2.db.message(id: a)
        #expect(row?.subject == "one!" && row?.isRead == true)
    }

    @Test func unreadFilterAndBusinessesRefresh() async throws {
        let bid = UUID()
        let rig = try Rig { req in
            req.path == "/v1/businesses"
              ? json(#"{"businesses":[{"id":"\#(bid.uuidString.lowercased())","name":"N","domain":"n.co","sort_order":0,"hidden":false,"enabled":true,"routing":"ok","unread":1,"last_message_at":null,"addresses":[{"local_part":"x","count":1,"last_at":null}]}]}"#)
              : json(#"{"messages":[],"next_cursor":null}"#)
        }
        try await rig.businesses.refresh()
        var it = rig.businesses.observe().makeAsyncIterator()
        let rows = await it.next()
        #expect(rows?.first?.name == "N" && rows?.first?.addresses.first?.localPart == "x")
        try await rig.mail.refresh(.init(businessId: bid, unreadOnly: true))
        let q = rig.server.requests.last!.url.query ?? ""
        #expect(q.contains("unread=true") && q.contains("business=\(bid.uuidString.lowercased())"))
    }

    @Test func optimisticArchiveThenFlushSendsBatchWithIdempotencyKey() async throws {
        let id = UUID()
        let rig = try Rig(echoBatch)
        try await rig.db.upsertMessages([MessageRecord(id: id, businessId: nil, receivedAt: .now)])
        try await rig.mail.setArchived(true, ids: [id])
        #expect(try await rig.db.message(id: id)?.archived == true)      // local immediately
        #expect(try await rig.db.pendingOps().count == 1)
        await rig.outbox.flush()
        let reqs = rig.batchRequests()
        #expect(reqs.count == 1 && reqs[0].json?["op"] as? String == "archive")
        #expect(reqs[0].headers["Idempotency-Key"]?.isEmpty == false)
        #expect(try await rig.db.pendingOps().isEmpty)
        #expect(try await rig.db.message(id: id)?.archived == true)
    }

    @Test func undoBeforeSendCancelsBothOps() async throws {
        let id = UUID()
        let rig = try Rig(echoBatch)
        try await rig.db.upsertMessages([MessageRecord(id: id, businessId: nil, receivedAt: .now)])
        try await rig.mail.setArchived(true, ids: [id])
        try await rig.mail.setArchived(false, ids: [id])
        #expect(try await rig.db.pendingOps().isEmpty)
        #expect(try await rig.db.message(id: id)?.archived == false)
        await rig.outbox.flush()
        #expect(rig.server.requests.isEmpty)
    }

    @Test func offlineGatesFlushThenReplaysWithSameKey() async throws {
        let id = UUID()
        let fail = Mutex(true)
        let rig = try Rig { req in
            if fail.withLock({ $0 }) { throw URLError(.notConnectedToInternet) }
            return echoBatch(req)
        }
        try await rig.db.upsertMessages([MessageRecord(id: id, businessId: nil, receivedAt: .now)])
        try await rig.mail.setRead(true, ids: [id])
        await rig.outbox.flush()                                // transport failure, op stays queued
        var ops = try await rig.db.pendingOps()
        #expect(ops.count == 1 && ops[0].attempts == 1 && ops[0].inFlight)
        fail.withLock { $0 = false }
        await rig.outbox.flush()                                // replay
        let keys = rig.batchRequests().compactMap { $0.headers["Idempotency-Key"] }
        #expect(keys.count == 2 && keys[0] == keys[1])
        ops = try await rig.db.pendingOps()
        #expect(ops.isEmpty)
    }

    @Test func reachabilityOfflineHoldsUntilOnline() async throws {
        let id = UUID()
        let server = MockServer(echoBatch)
        let db = try AppDatabase.inMemory()
        let reach = ManualReachability()
        let outbox = OutboxWorker(api: server.client(), db: db, reachability: reach, kickDelay: .milliseconds(10))
        let mail = MailRepository(api: server.client(), db: db, outbox: outbox)
        await outbox.start()
        reach.cont.yield(false)
        try await Task.sleep(for: .milliseconds(100))
        try await db.upsertMessages([MessageRecord(id: id, businessId: nil, receivedAt: .now)])
        try await mail.setRead(true, ids: [id])
        #expect(server.requests.isEmpty)
        reach.cont.yield(true)
        for _ in 0..<50 where !server.requests.isEmpty == false { try await Task.sleep(for: .milliseconds(50)) }
        #expect(server.requests.count == 1)
        await outbox.stop()
    }

    @Test func refreshDoesNotClobberPendingLocalState() async throws {
        let id = UUID()
        let rig = try Rig { _ in json(#"{"messages":[\#(messageJSON(id: id, read: false))],"next_cursor":null}"#) }
        try await rig.db.upsertMessages([MessageRecord(id: id, businessId: nil, receivedAt: .now)])
        // queue directly (no flush) so the op stays pending
        try await rig.db.enqueueOptimistic(kind: "read", inverse: "unread", ids: [id])
        try await rig.mail.refresh()
        #expect(try await rig.db.message(id: id)?.isRead == true)
    }

    @Test func permanentRejectionDropsOpAndRestoresServerState() async throws {
        let id = UUID()
        let rig = try Rig { req in
            req.path == "/v1/messages:batch"
              ? json(#"{"type":"about:blank","title":"Not Found","status":404,"code":"not_found"}"#, status: 404)
              : json("""
                {"message":\(messageJSON(id: id, read: false)),"thread":[],"attachments":[],"details":{"business":null,"address":null,"rule_name":null,"gmail":null,"labels":[],"size_bytes":null,"received_at":"2026-10-01T10:00:00Z"}}
                """)
        }
        try await rig.db.upsertMessages([MessageRecord(id: id, businessId: nil, receivedAt: .now)])
        try await rig.mail.setRead(true, ids: [id])
        await rig.outbox.flush()
        #expect(try await rig.db.pendingOps().isEmpty)
        #expect(try await rig.db.message(id: id)?.isRead == false)
    }

    @Test func batchGroupsSameKindIntoOneRequest() async throws {
        let ids = (0..<3).map { _ in UUID() }
        let rig = try Rig(echoBatch)
        try await rig.db.upsertMessages(ids.map { MessageRecord(id: $0, businessId: nil, receivedAt: .now) })
        try await rig.db.enqueueOptimistic(kind: "archive", inverse: "unarchive", ids: ids)
        await rig.outbox.flush()
        #expect(rig.batchRequests().count == 1)
        #expect((rig.batchRequests()[0].json?["ids"] as? [String])?.count == 3)
    }
}

@Suite struct EnvironmentTests {
    @Test func previewEnvironmentBuilds() throws {
        let env = try AppEnvironment.preview()
        #expect(env.api.baseURL.host == "preview.invalid")
    }
}
