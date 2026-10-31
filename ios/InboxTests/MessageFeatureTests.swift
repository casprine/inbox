import Testing
import Foundation
import API
import Persistence
import Domain
import Web
@testable import FeatureMessage

private let mid = MockData.messageID(1)
private let older = MockData.messageID(101)
private let attID = UUID(uuidString: "00000000-0000-4000-8000-0000000000B1")!

private func msg(_ id: UUID, out: Bool = false, name: String = "Northgate", from: String = "Billing@Northgate.example", ago: Int = 0, read: Bool = false) -> String {
    """
    {"id":"\(id.uuidString.lowercased())","business_id":null,"direction":"\(out ? "out" : "in")","from_addr":"\(from)","from_name":"\(name)",
     "subject":"Invoice","snippet":"snip","to_local":"invoices","to_tag":null,"received_at":"2026-10-0\(9 - ago)T10:00:00Z","is_read":\(read),
     "archived":false,"attachment_count":1,"thread_key":"t1","route":"routed"}
    """
}

private func detailJSON(thread: [String], gmail: String = #"{"state":"synced","last_error":null}"#, read: Bool = false) -> String {
    """
    {"message":\(msg(mid, read: read)),"thread":[\(thread.joined(separator: ","))],
     "attachments":[{"id":"\(attID.uuidString.lowercased())","filename":"Invoice-2041.pdf","mime":"application/pdf","size_bytes":86016,"content_id":null,"inline":false},
                    {"id":"\(UUID().uuidString.lowercased())","filename":"logo.png","mime":"image/png","size_bytes":10,"content_id":"logo","inline":true}],
     "details":{"business":{"id":"\(MockData.acme.uuidString.lowercased())","name":"Acme","domain":"acme-studio.com"},"address":"invoices@acme-studio.com",
                "rule_name":"Invoices","gmail":\(gmail),"labels":["Acme/Invoices"],"size_bytes":98304,"received_at":"2026-10-09T10:00:00Z"}}
    """
}

private func bodyJSON(html: String? = nil, text: String? = "Hello https://example.com", remote: Int = 0) -> String {
    let h = html.map { "\"\($0)\"" } ?? "null", t = text.map { "\"\($0)\"" } ?? "null"
    return #"{"html":\#(h),"text":\#(t),"remote_image_count":\#(remote),"auth_results":{"spf":"pass","dkim":"pass","dmarc":"fail"},"inline":[]}"#
}

@MainActor
private struct Rig {
    let server: MockServer, env: AppEnvironment
    init(_ handler: @escaping MockURLProtocol.Handler) throws {
        server = MockServer(handler)
        env = AppEnvironment(api: server.client(), tokenStore: InMemoryTokenStore("t"), database: try .inMemory(), reachability: ManualReachability())
    }
    func model() async throws -> MessageModel {
        try await MockData.seed(env.database)
        return MessageModel(env: env, id: mid)
    }
}

@MainActor @Suite struct MessageModelTests {
    static func router(thread: [String], remote: Int = 0, gmail: String = #"{"state":"synced","last_error":null}"#) -> MockURLProtocol.Handler {
        { r in
            if r.path == "/v1/messages/\(mid.uuidString.lowercased())" { return json(detailJSON(thread: thread, gmail: gmail)) }
            if r.path.hasSuffix("/body") { return json(bodyJSON(remote: remote)) }
            if r.path.hasSuffix("/raw") { return (200, [:], Data("From: a@b.co\r\nSubject: Hi\r\n there\r\n\r\nbody".utf8)) }
            if r.path.hasSuffix("gmail:retry") { return json(#"{"type":"about:blank","title":"nf","status":404,"code":"not_found"}"#, status: 404) }
            if r.path.hasPrefix("/v1/attachments/") { return (302, ["Location": "\(r.url.scheme!)://\(r.url.host!)/files/x"], Data()) }
            if r.path == "/files/x" { return (200, [:], Data("PDFDATA".utf8)) }
            return json("{}", status: 404)
        }
    }

    @Test func loadOpensLatestAndMarksReadLocally() async throws {
        let rig = try Rig(Self.router(thread: [msg(older, out: true, name: "Sam", ago: 2), msg(mid)]))
        let m = try await rig.model()
        await m.load()
        #expect(m.phase == .loaded)
        #expect(m.thread.map(\.id) == [older, mid])
        #expect(m.expanded == [mid])
        #expect(m.bodies[older] == nil)
        #expect(try await rig.env.database.message(id: mid)?.isRead == true)   // optimistic outbox write
        #expect(m.attachments.map(\.filename) == ["Invoice-2041.pdf"])         // inline cid parts are not chips
        #expect(m.toAddress(m.thread[1]) == "invoices@acme-studio.com")
        #expect(m.toAddress(m.thread[0]) == "invoices@acme-studio.com")
    }

    @Test func threadCollapseAndExpand() async throws {
        let rig = try Rig(Self.router(thread: [msg(older, out: true, name: "Sam", ago: 2), msg(mid)]))
        let m = try await rig.model(); await m.load()
        #expect(MessageFormat.senderName(m.thread[0]) == "Sam (you)")
        #expect(MessageFormat.senderName(m.thread[1]) == "Northgate")
        await m.toggle(older)
        #expect(m.expanded.contains(older))
        if case .ready = m.bodies[older] {} else { Issue.record("body should load on expand") }
        await m.toggle(older)
        #expect(!m.expanded.contains(older))
    }

    @Test func loadImagesIsPerSenderAndPersisted() async throws {
        let rig = try Rig(Self.router(thread: [msg(mid)], remote: 2))
        let m = try await rig.model(); await m.load()
        guard case .ready(let body) = m.bodies[mid], let message = m.detail?.message else { Issue.record("no body"); return }
        #expect(m.imagesBlocked(for: message, body: body))
        await m.loadImages(for: message)
        #expect(!m.imagesBlocked(for: message, body: body))
        let again = MessageModel(env: rig.env, id: mid); await again.load()   // fresh model reads the pref
        #expect(again.imagesAllowed(for: message))
        var other = message; other.fromAddr = "someone@else.co"
        #expect(!again.imagesAllowed(for: other))
        #expect(await ImageAllowList(rig.env.database).senders() == ["billing@northgate.example"])
    }

    @Test func missingMessageFails() async throws {
        let rig = try Rig { _ in json("{}", status: 404) }
        let m = try await rig.model(); await m.load()
        #expect(m.phase == .failed("This message is no longer stored."))
    }

    @Test func gmailRetryHandles404Gracefully() async throws {
        let rig = try Rig(Self.router(thread: [msg(mid)], gmail: #"{"state":"not_found","last_error":null,"attempts":6}"#))
        let m = try await rig.model(); await m.load()
        #expect(m.gmailState == "not_found")
        await m.retryGmail()
        #expect(m.notice == "Retry isn't available on this server yet")
        #expect(rig.server.requests.contains { $0.method == "POST" && $0.path.hasSuffix("/gmail:retry") })
    }

    @Test func attachmentDownloadsToTempFile() async throws {
        let rig = try Rig(Self.router(thread: [msg(mid)]))
        let m = try await rig.model(); await m.load()
        let url = try #require(await m.downloadAttachment(m.attachments[0]))
        #expect(url.lastPathComponent == "Invoice-2041.pdf")
        #expect(try Data(contentsOf: url) == Data("PDFDATA".utf8))
    }

    @Test func markUnreadQueuesOp() async throws {
        let rig = try Rig(Self.router(thread: [msg(mid)]))
        let m = try await rig.model(); await m.load()
        await m.markUnread()
        #expect(m.notice == "Marked as unread")
        #expect(try await rig.env.database.message(id: mid)?.isRead == false)
    }

    @Test func detailsModelAndOriginal() async throws {
        let rig = try Rig(Self.router(thread: [msg(mid)]))
        let d = MessageDetailsModel(env: rig.env, id: mid)
        await d.loadDetails()
        #expect(d.detail?.details.ruleName == "Invoices")
        await d.loadOriginal()
        #expect(d.raw?.headers.map(\.name) == ["From", "Subject"])
        #expect(d.raw?.headers[1].value == "Hi there")
        #expect(d.raw?.source == "body")
        #expect(d.auth?.dmarc == "fail")
    }

    @Test func gmailLinksAndPlainStatus() {
        #expect(GmailLinks.web(gmailID: "18f2").absoluteString == "https://mail.google.com/mail/u/0/#all/18f2")
        #expect(GmailLinks.web(gmailID: nil).absoluteString == "https://mail.google.com/mail/u/0/#all")
        #expect(GmailLinks.app.absoluteString == "googlegmail://")
        #expect(MessageFormat.gmailStatus("not_found") == "Not found")
    }

    @Test func mockModeServesReaderContent() async throws {
        let env = try await AppEnvironment.mock()
        let m = MessageModel(env: env, id: MockContent.kettleID)
        try await MockData.seed(env.database, includeReader: true)
        await m.load()
        guard case .ready(let b) = m.bodies[MockContent.kettleID] else { Issue.record("no mock body"); return }
        #expect(b.remoteImageCount == 2)
        let inv = MessageModel(env: env, id: MockContent.threadID); await inv.load()
        #expect(inv.thread.count == 4 && inv.thread.first?.direction == "out")
    }
}

// MARK: Offline cache (S-2)

@MainActor @Suite struct OfflineCacheTests {
    @Test func openedMessageIsSavedAndShownWhenTheServerIsUnreachable() async throws {
        let online = Mutex<Bool>(true)
        let router = MessageModelTests.router(thread: [msg(mid)])
        let rig = try Rig { r in
            if !online.withLock({ $0 }) { throw URLError(.notConnectedToInternet) }
            return try router(r)
        }
        let first = try await rig.model()
        await first.load()
        #expect(first.phase == .loaded && !first.isOffline)
        #expect(try await rig.env.database.cacheKeys().contains("detail:\(mid.uuidString)"))

        online.withLock { $0 = false }
        let second = MessageModel(env: rig.env, id: mid)
        await second.load()
        #expect(second.phase == .loaded && second.isOffline)
        #expect(second.detail?.details.business?.name == "Acme" && second.attachments.map(\.filename) == ["Invoice-2041.pdf"])
        if case .ready(let b) = second.bodies[mid] { #expect(b.text == "Hello https://example.com") } else { Issue.record("body not shown offline") }
    }

    @Test func nothingSavedMeansTheNormalError_andRealErrorsNeverFallBack() async throws {
        let rig = try Rig { _ in throw URLError(.notConnectedToInternet) }
        let m = try await rig.model()
        await m.load()
        if case .failed = m.phase {} else { Issue.record("expected a failure without a saved copy") }
        // A 404 (message deleted on the server) must not resurrect the saved copy.
        let ok = Mutex<Bool>(true)
        let router = MessageModelTests.router(thread: [msg(mid)])
        let rig2 = try Rig { r in ok.withLock { $0 } ? try router(r) : json(#"{"type":"about:blank","title":"nf","status":404,"code":"not_found"}"#, status: 404) }
        let a = try await rig2.model(); await a.load()
        ok.withLock { $0 = false }
        let b = MessageModel(env: rig2.env, id: mid); await b.load()
        if case .failed = b.phase { #expect(!b.isOffline) } else { Issue.record("a 404 must show the error") }
        #expect(MessageModel.isConnectivity(APIError.http(status: 503)) && !MessageModel.isConnectivity(APIError.http(status: 404)))
    }

    @Test func cacheEvictsLeastRecentlyUsedPastTheCap() async throws {
        let db = try AppDatabase.inMemory()
        let t0 = Date(timeIntervalSince1970: 1_000)
        try await db.cachePut("a", json: String(repeating: "x", count: 40), now: t0, capBytes: 100)
        try await db.cachePut("b", json: String(repeating: "y", count: 40), now: t0.addingTimeInterval(1), capBytes: 100)
        _ = try await db.cacheGet("a", now: t0.addingTimeInterval(2))            // a is now newer than b
        try await db.cachePut("c", json: String(repeating: "z", count: 40), now: t0.addingTimeInterval(3), capBytes: 100)
        #expect(Set(try await db.cacheKeys()) == ["a", "c"])
        #expect(try await db.cacheBytes() == 80)
    }

    @Test func retentionPurgeAndDeleteEverythingClearTheCache() async throws {
        let db = try AppDatabase.inMemory()
        let now = Date(timeIntervalSince1970: 10_000_000)
        try await db.cachePut("old", json: "{}", now: now.addingTimeInterval(-100 * 86_400))
        try await db.cachePut("new", json: "{}", now: now.addingTimeInterval(-10 * 86_400))
        #expect(try await db.cachePurge(fetchedBefore: now.addingTimeInterval(-90 * 86_400)) == 1)
        #expect(try await db.cacheKeys() == ["new"])
        try await db.clearMailCache()                                            // what "Delete everything" runs
        #expect(try await db.cacheKeys().isEmpty)
    }
}
