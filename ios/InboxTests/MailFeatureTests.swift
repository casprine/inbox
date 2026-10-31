import Testing
import Foundation
import API
import Persistence
import Domain
@testable import FeatureMail

@MainActor
private struct Rig {
    let server: MockServer, env: AppEnvironment
    init(_ handler: @escaping MockURLProtocol.Handler = { _ in json(#"{"messages":[]}"#) }) throws {
        server = MockServer(handler)
        env = AppEnvironment(api: server.client(), tokenStore: InMemoryTokenStore("t"), database: try .inMemory(), reachability: ManualReachability())
    }
    func model(_ scope: MessageListModel.Scope, address: String? = nil, toast: Duration = .seconds(5)) async throws -> MessageListModel {
        try await MockData.seed(env.database)
        let m = MessageListModel(env: env, scope: scope, selectedAddress: address, toastDuration: toast)
        await m.loadCache()
        return m
    }
    var batches: [CapturedRequest] { server.requests.filter { $0.path == "/v1/messages:batch" } }
}

@MainActor @Suite struct MessageListModelTests {
    @Test func businessListIsFlatNewestFirst() async throws {
        let m = try await Rig().model(.business(MockData.acme))
        let ids = m.visibleMessages.map(\.id)
        #expect(ids.count == 7)
        #expect(m.visibleMessages.map(\.receivedAt) == m.visibleMessages.map(\.receivedAt).sorted(by: >))
        #expect(m.sections.count == 1 && m.sections[0].title == nil)
    }

    @Test func chipFiltering() async throws {
        let m = try await Rig().model(.business(MockData.acme))
        #expect(m.chips.map(\.title) == ["All", "invoices@", "hello@", "press@"])
        #expect(m.chips.first?.count == 7)
        m.selectedAddress = "hello"
        #expect(m.visibleMessages.count == 3 && m.visibleMessages.allSatisfy { $0.toLocal == "hello" })
        m.selectedAddress = nil
        #expect(m.visibleMessages.count == 7)
        let preselected = try await Rig().model(.business(MockData.acme), address: "press")
        #expect(preselected.visibleMessages.count == 2)
    }

    @Test func allBusinessesGroupsByBusiness() async throws {
        let m = try await Rig().model(.all)
        #expect(m.sections.map(\.title) == ["Acme Studio", "Northgate Ltd", "Lumen Hosting"])
        #expect(m.totalUnread == 3)
    }

    @Test func unreadFilterAndEmptyKinds() async throws {
        let m = try await Rig().model(.business(MockData.lumen))
        #expect(!m.visibleMessages.isEmpty)
        m.unreadOnly = true
        #expect(m.visibleMessages.isEmpty)
        let r = try Rig()
        let m2 = MessageListModel(env: r.env, scope: .business(MockData.lumen))
        await m2.refresh()
        #expect(m2.isEmpty && m2.emptyKind == .noMail)
        #expect(m.emptyKind == .allRead)
    }

    @Test func selectionFlow() async throws {
        let m = try await Rig().model(.business(MockData.acme))
        let a = MockData.messageID(1), b = MockData.messageID(2)
        m.beginSelecting(a)
        #expect(m.isSelecting && m.selection == [a])
        m.toggleSelection(b); #expect(m.selection == [a, b])
        m.toggleSelection(a); #expect(m.selection == [b])
        m.selectAll(); #expect(m.selection.count == 7)
        m.endSelecting(); #expect(!m.isSelecting && m.selection.isEmpty)
    }

    @Test func toggleReadMarksThenUnmarks() async throws {
        let rig = try Rig()
        let m = try await rig.model(.business(MockData.acme))
        let id = MockData.messageID(1)
        await m.toggleRead([id])
        await m.loadCache()
        #expect(m.messages.first { $0.id == id }?.isRead == true)
        await m.toggleRead([id])
        await m.loadCache()
        #expect(m.messages.first { $0.id == id }?.isRead == false)
    }

    @Test func archiveThenUndoCancelsQueuedOp() async throws {
        let rig = try Rig()
        let m = try await rig.model(.business(MockData.acme))
        let id = MockData.messageID(1)
        m.beginSelecting(id)
        await m.archive(m.selection)
        #expect(m.toast?.message == "Archived" && !m.isSelecting)
        await m.loadCache()
        #expect(!m.messages.contains { $0.id == id })
        #expect(try await rig.env.database.pendingOps().count == 1)
        await m.undo()
        #expect(m.toast == nil)
        await m.loadCache()
        #expect(m.messages.contains { $0.id == id })
        #expect(try await rig.env.database.pendingOps().isEmpty)
        #expect(rig.batches.isEmpty)
    }

    @Test func toastDismissesAfterDuration() async throws {
        let m = try await Rig().model(.business(MockData.acme), toast: .milliseconds(50))
        await m.archive([MockData.messageID(1), MockData.messageID(2)])
        #expect(m.toast?.message == "2 archived")
        try await Task.sleep(for: .milliseconds(300))
        #expect(m.toast == nil)
    }

    @Test func refreshPaginates() async throws {
        let a = UUID(), b = UUID()
        let rig = try Rig { req in
            if req.url.query?.contains("cursor=c1") == true { return json(#"{"messages":[\#(messageJSON(id: b, subject: "old", at: "2026-09-01T10:00:00Z"))]}"#) }
            return json(#"{"messages":[\#(messageJSON(id: a, subject: "new"))],"next_cursor":"c1"}"#)
        }
        let m = MessageListModel(env: rig.env, scope: .all)
        await m.refresh()
        #expect(m.nextCursor == "c1")
        await m.loadMore()
        #expect(m.nextCursor == nil)
        await m.loadCache()
        #expect(m.messages.map(\.id) == [a, b])
    }
}

@MainActor @Suite struct BusinessesModelTests {
    @Test func attentionOnlyWhenCountsPositive() async throws {
        let rig = try Rig()
        let m = BusinessesModel(env: rig.env)
        await m.loadCache()
        #expect(!m.showsAttention && m.isLoading)
        try await MockData.seed(rig.env.database)
        await m.loadCache()
        #expect(m.showsAttention && m.unroutedCount == 3 && m.failedCount == 1)
        #expect(m.visible.count == 3 && m.totalUnread == 3 && !m.isLoading)
        try await rig.env.database.setPref("attention.unrouted", "0")
        try await rig.env.database.setPref("attention.failed", "0")
        await m.loadCache()
        #expect(!m.showsAttention)
    }
}

@MainActor @Suite struct LiveEventsTests {
    @Test func mailNewBumpsTick() {
        let live = LiveEvents()
        live.handle(.event(ServerEvent.decode(Data(#"{"type":"mail.new","message_id":"x"}"#.utf8))!))
        live.handle(.event(.healthChanged([:])))
        live.handle(.reconnected)
        #expect(live.tick == 2)
    }
}

@Suite struct MailTimeTests {
    @Test func formats() {
        var cal = Calendar(identifier: .gregorian); cal.timeZone = TimeZone(identifier: "UTC")!
        let loc = Locale(identifier: "en_US")
        let now = ISO8601DateFormatter().date(from: "2026-10-11T12:00:00Z")!
        func s(_ iso: String) -> String { MailTime.string(ISO8601DateFormatter().date(from: iso)!, now: now, calendar: cal, locale: loc) }
        #expect(s("2026-10-11T09:41:00Z").hasPrefix("9:41"))
        #expect(s("2026-10-10T20:00:00Z") == "Yesterday")
        #expect(s("2026-10-07T20:00:00Z") == "Wed")
        #expect(s("2026-09-20T20:00:00Z") == "Sep 20")
    }
}
