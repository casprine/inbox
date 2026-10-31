import Foundation
import API
import Persistence

/// Businesses: observe the cache, refresh from the network (stale-while-revalidate).
public actor BusinessRepository {
    private let api: APIClient
    private let db: AppDatabase
    public init(api: APIClient, db: AppDatabase) { self.api = api; self.db = db }

    /// Emits the cached list immediately, then again on every change.
    public nonisolated func observe() -> AsyncStream<[BusinessRecord]> { db.observeBusinesses() }

    public func refresh() async throws {
        let list = try await api.businesses()
        try await db.replaceBusinesses(list.map(BusinessRecord.init))
    }

    /// Every business including hidden and disabled ones (Settings → Businesses).
    public func all() async throws -> [BusinessRecord] { try await db.businesses() }

    public func rename(id: UUID, to name: String) async throws {
        _ = try await api.patchBusiness(id: id, PatchBusinessRequest(name: name)); try await refresh()
    }
    /// Hides or shows a business: the cache changes first (the Mail list updates at once), the server follows; a failure restores it.
    public func setHidden(_ hidden: Bool, id: UUID) async throws {
        let before = try await db.businesses()
        var rows = before
        if let i = rows.firstIndex(where: { $0.id == id }) { rows[i].hidden = hidden }
        try await db.replaceBusinesses(rows)
        do { _ = try await api.patchBusiness(id: id, PatchBusinessRequest(hidden: hidden)) }
        catch { try? await db.replaceBusinesses(before); throw error }
    }
    /// Applies the new order locally first so the list doesn't jump back, then tells the server.
    public func reorder(ids: [UUID]) async throws {
        let before = try await db.businesses()
        let byID = Dictionary(uniqueKeysWithValues: before.map { ($0.id, $0) })
        var moved = ids.compactMap { byID[$0] }
        for (i, _) in moved.enumerated() { moved[i].sortOrder = i }
        try await db.replaceBusinesses(moved + before.filter { b in !ids.contains(b.id) })
        do { try await db.replaceBusinesses(try await api.orderBusinesses(ids: ids).map(BusinessRecord.init)) }
        catch { try? await db.replaceBusinesses(before); throw error }
    }
    /// Deletes the business and its stored copies on the server (Gmail untouched), then drops it from the cache.
    public func remove(id: UUID) async throws {
        try await api.deleteBusiness(id: id); try await refresh()
    }
    public func add(name: String, domain: String, zoneId: String?) async throws {
        _ = try await api.createBusiness(CreateBusinessRequest(name: name, domain: domain, cfZoneId: zoneId)); try await refresh()
    }
}

public actor MailRepository {
    private let api: APIClient
    private let db: AppDatabase
    private let outbox: OutboxWorker
    public init(api: APIClient, db: AppDatabase, outbox: OutboxWorker) { self.api = api; self.db = db; self.outbox = outbox }

    /// Cached list (renders instantly), re-emitted whenever refresh/optimistic writes change it.
    public nonisolated func observe(_ filter: MessageFilter = .init()) -> AsyncStream<[MessageRecord]> { db.observeMessages(filter) }

    /// Fetches one page and upserts it. Returns the next cursor (nil on the last page).
    @discardableResult
    public func refresh(_ filter: MessageFilter = .init(), cursor: String? = nil, limit: Int = 50) async throws -> String? {
        var q = filter.query
        q.cursor = cursor; q.limit = limit
        let page = try await api.messages(q)
        try await db.upsertMessages(page.messages.map(MessageRecord.init))
        return page.nextCursor
    }

    public func setRead(_ read: Bool, ids: [UUID]) async throws {
        try await db.enqueueOptimistic(kind: read ? "read" : "unread", inverse: read ? "unread" : "read", ids: ids)
        await outbox.kick()
    }

    public func setArchived(_ archived: Bool, ids: [UUID]) async throws {
        try await db.enqueueOptimistic(kind: archived ? "archive" : "unarchive", inverse: archived ? "unarchive" : "archive", ids: ids)
        await outbox.kick()
    }
}
