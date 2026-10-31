import Foundation
import GRDB

public struct MessageFilter: Sendable, Hashable {
    public var businessId: UUID?
    public var unreadOnly: Bool
    public var includeArchived: Bool
    public init(businessId: UUID? = nil, unreadOnly: Bool = false, includeArchived: Bool = false) {
        self.businessId = businessId; self.unreadOnly = unreadOnly; self.includeArchived = includeArchived
    }
}

/// SQLite cache + outbox (ENGINEERING_PLAN §8.2). `DatabaseQueue` serialises access; safe to share.
public final class AppDatabase: Sendable {
    public let writer: DatabaseQueue

    public init(_ queue: DatabaseQueue) throws {
        writer = queue
        try Self.migrator.migrate(queue)
    }

    public static func inMemory() throws -> AppDatabase { try AppDatabase(DatabaseQueue()) }

    public static func open(at url: URL) throws -> AppDatabase {
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        return try AppDatabase(DatabaseQueue(path: url.path))
    }

    static var migrator: DatabaseMigrator {
        var m = DatabaseMigrator()
        m.registerMigration("v1") { db in
            try db.create(table: "business") { t in
                t.primaryKey("id", .text)
                t.column("name", .text).notNull()
                t.column("domain", .text).notNull()
                t.column("sortOrder", .integer).notNull()
                t.column("hidden", .boolean).notNull()
                t.column("enabled", .boolean).notNull()
                t.column("routing", .text).notNull()
                t.column("unread", .integer).notNull()
                t.column("lastMessageAt", .datetime)
                t.column("addressesJSON", .text).notNull().defaults(to: "[]")
            }
            try db.create(table: "message") { t in
                t.primaryKey("id", .text)
                t.column("businessId", .text).indexed()
                t.column("direction", .text).notNull()
                t.column("fromAddr", .text)
                t.column("fromName", .text)
                t.column("subject", .text)
                t.column("snippet", .text)
                t.column("toLocal", .text)
                t.column("toTag", .text)
                t.column("receivedAt", .datetime).notNull().indexed()
                t.column("isRead", .boolean).notNull()
                t.column("archived", .boolean).notNull()
                t.column("attachmentCount", .integer).notNull()
                t.column("threadKey", .text).notNull().indexed()
                t.column("route", .text).notNull()
            }
            try db.create(table: "message_body") { t in
                t.primaryKey("messageId", .text)
                t.column("htmlPath", .text)
                t.column("text", .text)
                t.column("remoteImageCount", .integer).notNull()
                t.column("byteSize", .integer).notNull()
                t.column("fetchedAt", .datetime).notNull()
                t.column("lastAccessedAt", .datetime).notNull().indexed()
            }
            try db.create(table: "thread_item") { t in
                t.column("threadKey", .text).notNull()
                t.column("messageId", .text).notNull()
                t.column("position", .integer).notNull()
                t.primaryKey(["threadKey", "messageId"])
            }
            try db.create(table: "pref") { t in
                t.primaryKey("key", .text)
                t.column("value", .text).notNull()
            }
            try db.create(table: "recent_search") { t in
                t.primaryKey("q", .text)
                t.column("searchedAt", .datetime).notNull()
            }
            try db.create(table: "pending_op") { t in
                t.autoIncrementedPrimaryKey("id")
                t.column("kind", .text).notNull()
                t.column("messageId", .text).notNull().indexed()
                t.column("idempotencyKey", .text).notNull()
                t.column("inFlight", .boolean).notNull().defaults(to: false)
                t.column("attempts", .integer).notNull().defaults(to: 0)
                t.column("lastError", .text)
                t.column("createdAt", .datetime).notNull()
            }
        }
        m.registerMigration("v2_rule") { db in
            try db.create(table: "rule") { t in
                t.primaryKey("id", .text)
                t.column("name", .text).notNull()
                t.column("position", .integer).notNull().indexed()
                t.column("enabled", .boolean).notNull()
                t.column("businessId", .text)
                t.column("matchToLocal", .text)
                t.column("matchFrom", .text)
                t.column("matchSubject", .text)
                t.column("gmailLabel", .text)
                t.column("gmailArchive", .boolean).notNull()
                t.column("gmailMarkRead", .boolean).notNull()
                t.column("labelOnly", .boolean).notNull()
                t.column("importedFromFilter", .text)
                t.column("messageCount", .integer).notNull().defaults(to: 0)
            }
        }
        // Offline cache of opened messages (detail + bodies), LRU by `lastAccessedAt`, capped by bytes.
        m.registerMigration("v3_cache") { db in
            try db.create(table: "cache_entry") { t in
                t.primaryKey("key", .text)
                t.column("json", .text).notNull()
                t.column("bytes", .integer).notNull()
                t.column("fetchedAt", .datetime).notNull().indexed()
                t.column("lastAccessedAt", .datetime).notNull().indexed()
            }
        }
        return m
    }

    // MARK: Offline cache

    /// ~200 MB (ENGINEERING_PLAN §8.2); `put` evicts the least recently used entries beyond it.
    public static let cacheCapBytes = 200 * 1024 * 1024

    public func cachePut(_ key: String, json: String, now: Date = .now, capBytes: Int = AppDatabase.cacheCapBytes) async throws {
        let size = json.utf8.count
        try await writer.write { db in
            try db.execute(sql: "INSERT OR REPLACE INTO cache_entry (key, json, bytes, fetchedAt, lastAccessedAt) VALUES (?, ?, ?, ?, ?)",
                           arguments: [key, json, size, now, now])
            var total = try Int.fetchOne(db, sql: "SELECT COALESCE(SUM(bytes), 0) FROM cache_entry") ?? 0
            while total > capBytes {
                guard let row = try Row.fetchOne(db, sql: "SELECT key, bytes FROM cache_entry WHERE key != ? ORDER BY lastAccessedAt ASC LIMIT 1", arguments: [key]) else { break }
                try db.execute(sql: "DELETE FROM cache_entry WHERE key = ?", arguments: [row["key"] as String])
                total -= row["bytes"] as Int
            }
        }
    }

    /// Returns the cached JSON and marks it recently used.
    public func cacheGet(_ key: String, now: Date = .now) async throws -> String? {
        try await writer.write { db in
            guard let json = try String.fetchOne(db, sql: "SELECT json FROM cache_entry WHERE key = ?", arguments: [key]) else { return nil }
            try db.execute(sql: "UPDATE cache_entry SET lastAccessedAt = ? WHERE key = ?", arguments: [now, key])
            return json
        }
    }

    public func cacheBytes() async throws -> Int { try await writer.read { db in try Int.fetchOne(db, sql: "SELECT COALESCE(SUM(bytes), 0) FROM cache_entry") ?? 0 } }
    public func cacheKeys() async throws -> [String] { try await writer.read { db in try String.fetchAll(db, sql: "SELECT key FROM cache_entry ORDER BY lastAccessedAt DESC") } }

    /// Retention: drops entries fetched before `cutoff` (the server prunes mail older than `retention_days`; the phone follows).
    @discardableResult
    public func cachePurge(fetchedBefore cutoff: Date) async throws -> Int {
        try await writer.write { db in
            try db.execute(sql: "DELETE FROM cache_entry WHERE fetchedAt < ?", arguments: [cutoff])
            return db.changesCount
        }
    }

    // MARK: Rules

    public func replaceRules(_ rows: [RuleRecord]) async throws {
        try await writer.write { db in
            let keep = rows.map(\.id)
            try RuleRecord.filter(!keep.contains(Column("id"))).deleteAll(db)
            for r in rows { try r.save(db) }
        }
    }

    public func rules() async throws -> [RuleRecord] {
        try await writer.read { db in try RuleRecord.order(Column("position"), Column("id")).fetchAll(db) }
    }

    public func observeRules() -> AsyncStream<[RuleRecord]> {
        stream(ValueObservation.tracking { db in try RuleRecord.order(Column("position"), Column("id")).fetchAll(db) })
    }

    public func saveRule(_ r: RuleRecord) async throws { try await writer.write { db in try r.save(db) } }

    public func deleteRule(id: UUID) async throws { try await writer.write { db in _ = try RuleRecord.deleteOne(db, key: id) } }

    public func setRuleEnabled(id: UUID, _ enabled: Bool) async throws {
        try await writer.write { db in
            try db.execute(sql: "UPDATE rule SET enabled = ? WHERE id = ?", arguments: [enabled, id])
        }
    }

    /// Rewrites positions so `ids` come first in the given order, then any others in their current order.
    public func reorderRules(ids: [UUID]) async throws {
        try await writer.write { db in
            let all = try RuleRecord.order(Column("position"), Column("id")).fetchAll(db)
            let byID = Dictionary(uniqueKeysWithValues: all.map { ($0.id, $0) })
            let head = ids.compactMap { byID[$0] }
            let headIDs = Set(head.map(\.id))
            for (i, var r) in (head + all.filter { !headIDs.contains($0.id) }).enumerated() {
                r.position = i
                try r.update(db)
            }
        }
    }

    // MARK: Businesses

    public func replaceBusinesses(_ rows: [BusinessRecord]) async throws {
        try await writer.write { db in
            let keep = rows.map(\.id)
            try BusinessRecord.filter(!keep.contains(Column("id"))).deleteAll(db)
            for r in rows { try r.save(db) }
        }
    }

    public func businesses() async throws -> [BusinessRecord] {
        try await writer.read { db in try BusinessRecord.order(Column("sortOrder")).fetchAll(db) }
    }

    public func observeBusinesses() -> AsyncStream<[BusinessRecord]> {
        stream(ValueObservation.tracking { db in try BusinessRecord.order(Column("sortOrder")).fetchAll(db) })
    }

    // MARK: Messages

    private func messageRequest(_ f: MessageFilter) -> QueryInterfaceRequest<MessageRecord> {
        var req = MessageRecord.all()
        if let b = f.businessId { req = req.filter(Column("businessId") == b) }
        if f.unreadOnly { req = req.filter(Column("isRead") == false) }
        if !f.includeArchived { req = req.filter(Column("archived") == false) }
        return req.order(Column("receivedAt").desc, Column("id").desc)
    }

    public func messages(_ f: MessageFilter = .init()) async throws -> [MessageRecord] {
        let req = messageRequest(f)
        return try await writer.read { db in try req.fetchAll(db) }
    }

    public func observeMessages(_ f: MessageFilter = .init()) -> AsyncStream<[MessageRecord]> {
        let req = messageRequest(f)
        return stream(ValueObservation.tracking { db in try req.fetchAll(db) })
    }

    /// Upserts server rows. For ids with a pending op the local `isRead`/`archived` win, so a refresh
    /// racing an optimistic write cannot flicker it back.
    public func upsertMessages(_ rows: [MessageRecord]) async throws {
        try await writer.write { db in
            let pending = Set(try PendingOp.fetchAll(db).map(\.messageId))
            for var r in rows {
                if pending.contains(r.id), let local = try MessageRecord.fetchOne(db, key: r.id) {
                    r.isRead = local.isRead; r.archived = local.archived
                }
                try r.save(db)
            }
        }
    }

    public func message(id: UUID) async throws -> MessageRecord? { try await writer.read { db in try MessageRecord.fetchOne(db, key: id) } }

    // MARK: Outbox

    /// Applies `kind` ("read"|"unread"|"archive"|"unarchive") locally and queues it. If the inverse op for the
    /// same message is still queued and unsent, both cancel (undo before send) and nothing is queued.
    public func enqueueOptimistic(kind: String, inverse: String, ids: [UUID], key: @Sendable () -> String = { UUID().uuidString }) async throws {
        let k = key
        try await writer.write { db in
            for id in ids {
                guard var m = try MessageRecord.fetchOne(db, key: id) else { continue }
                switch kind {
                case "read": m.isRead = true
                case "unread": m.isRead = false
                case "archive": m.archived = true
                case "unarchive": m.archived = false
                default: break
                }
                try m.update(db)
                if let cancel = try PendingOp.filter(Column("messageId") == id && Column("kind") == inverse && Column("inFlight") == false)
                    .order(Column("id").desc).fetchOne(db) {
                    _ = try cancel.delete(db)
                } else {
                    var op = PendingOp(kind: kind, messageId: id, idempotencyKey: k())
                    try op.insert(db)
                }
            }
        }
    }

    public func pendingOps() async throws -> [PendingOp] {
        try await writer.read { db in try PendingOp.order(Column("id")).fetchAll(db) }
    }

    /// Takes the next batch of rows sharing a kind (oldest first, up to `limit`), marks them in flight under
    /// one shared idempotency key and returns them. Rows already in flight are returned first, as-is, so a
    /// retry after a transport failure re-sends the identical request.
    public func claimNextBatch(limit: Int = 200, newKey: String = UUID().uuidString) async throws -> [PendingOp] {
        try await writer.write { db in
            if let first = try PendingOp.filter(Column("inFlight") == true).order(Column("id")).fetchOne(db) {
                return try PendingOp.filter(Column("inFlight") == true && Column("idempotencyKey") == first.idempotencyKey)
                    .order(Column("id")).fetchAll(db)
            }
            guard let first = try PendingOp.order(Column("id")).fetchOne(db) else { return [] }
            var rows = try PendingOp.filter(Column("kind") == first.kind).order(Column("id")).limit(limit).fetchAll(db)
            // One row per message per request; duplicates stay queued for the next batch.
            var seen = Set<UUID>()
            rows = rows.filter { seen.insert($0.messageId).inserted }
            for i in rows.indices {
                rows[i].inFlight = true; rows[i].idempotencyKey = newKey
                try rows[i].update(db)
            }
            return rows
        }
    }

    public func completeOps(_ ids: [Int64]) async throws {
        try await writer.write { db in _ = try PendingOp.deleteAll(db, keys: ids) }
    }

    public func recordFailure(_ ids: [Int64], error: String) async throws {
        try await writer.write { db in
            for id in ids {
                guard var op = try PendingOp.fetchOne(db, key: id) else { continue }
                op.attempts += 1; op.lastError = error
                try op.update(db)
            }
        }
    }

    // MARK: Prefs / recents

    /// Drops every cached message, body, thread row and pending op, and the search history (after "Delete everything").
    /// Businesses, rules and prefs stay: only stored mail copies were deleted on the server.
    public func clearMailCache() async throws {
        try await writer.write { db in
            for t in ["thread_item", "message_body", "pending_op", "recent_search", "message", "cache_entry"] { try db.execute(sql: "DELETE FROM \(t)") }
            try db.execute(sql: "UPDATE business SET unread = 0, lastMessageAt = NULL")
        }
    }

    public func pref(_ key: String) async throws -> String? { try await writer.read { db in try PrefRecord.fetchOne(db, key: key)?.value } }
    public func setPref(_ key: String, _ value: String) async throws { try await writer.write { db in try PrefRecord(key: key, value: value).save(db) } }

    public func replaceRecentSearches(_ qs: [String]) async throws {
        try await writer.write { db in
            try RecentSearchRecord.deleteAll(db)
            let now = Date.now
            for (i, q) in qs.enumerated() { try RecentSearchRecord(q: q, searchedAt: now.addingTimeInterval(-Double(i))).save(db) }
        }
    }

    public func recentSearches() async throws -> [String] {
        try await writer.read { db in try RecentSearchRecord.order(Column("searchedAt").desc).fetchAll(db).map(\.q) }
    }

    // MARK: Bodies / threads

    public func saveBody(_ r: MessageBodyRecord) async throws { try await writer.write { db in try r.save(db) } }
    public func body(messageId: UUID) async throws -> MessageBodyRecord? { try await writer.read { db in try MessageBodyRecord.fetchOne(db, key: messageId) } }

    public func replaceThread(key: String, messageIds: [UUID]) async throws {
        try await writer.write { db in
            try ThreadItemRecord.filter(Column("threadKey") == key).deleteAll(db)
            for (i, id) in messageIds.enumerated() { try ThreadItemRecord(threadKey: key, messageId: id, position: i).insert(db) }
        }
    }

    // MARK: Observation helper

    private func stream<T: Sendable>(_ obs: ValueObservation<ValueReducers.Fetch<T>>) -> AsyncStream<T> {
        let queue = writer
        return AsyncStream { cont in
            let task = Task {
                do { for try await v in obs.values(in: queue) { cont.yield(v) } } catch {}
                cont.finish()
            }
            cont.onTermination = { _ in task.cancel() }
        }
    }
}
