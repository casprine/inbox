import Foundation
import GRDB

public struct BusinessRecord: Codable, Sendable, Hashable, Identifiable, FetchableRecord, PersistableRecord {
    public static let databaseTableName = "business"
    public var id: UUID
    public var name: String
    public var domain: String
    public var sortOrder: Int
    public var hidden: Bool
    public var enabled: Bool
    public var routing: String
    public var unread: Int
    public var lastMessageAt: Date?
    /// JSON-encoded `[{local_part,count,last_at}]`; opaque to this layer.
    public var addressesJSON: String
    public init(id: UUID, name: String, domain: String, sortOrder: Int, hidden: Bool, enabled: Bool, routing: String,
                unread: Int, lastMessageAt: Date?, addressesJSON: String = "[]") {
        self.id = id; self.name = name; self.domain = domain; self.sortOrder = sortOrder; self.hidden = hidden
        self.enabled = enabled; self.routing = routing; self.unread = unread; self.lastMessageAt = lastMessageAt
        self.addressesJSON = addressesJSON
    }
}

/// List fields only. `isRead`/`archived` hold the optimistic (local) value while a pending_op exists.
public struct MessageRecord: Codable, Sendable, Hashable, Identifiable, FetchableRecord, PersistableRecord {
    public static let databaseTableName = "message"
    public var id: UUID
    public var businessId: UUID?
    public var direction: String
    public var fromAddr: String?
    public var fromName: String?
    public var subject: String?
    public var snippet: String?
    public var toLocal: String?
    public var toTag: String?
    public var receivedAt: Date
    public var isRead: Bool
    public var archived: Bool
    public var attachmentCount: Int
    public var threadKey: String
    public var route: String
    public init(id: UUID, businessId: UUID?, direction: String = "in", fromAddr: String? = nil, fromName: String? = nil,
                subject: String? = nil, snippet: String? = nil, toLocal: String? = nil, toTag: String? = nil,
                receivedAt: Date, isRead: Bool = false, archived: Bool = false, attachmentCount: Int = 0,
                threadKey: String = "", route: String = "routed") {
        self.id = id; self.businessId = businessId; self.direction = direction; self.fromAddr = fromAddr
        self.fromName = fromName; self.subject = subject; self.snippet = snippet; self.toLocal = toLocal
        self.toTag = toTag; self.receivedAt = receivedAt; self.isRead = isRead; self.archived = archived
        self.attachmentCount = attachmentCount; self.threadKey = threadKey; self.route = route
    }
}

/// Cache metadata for a fetched body. HTML itself lives on disk (`htmlPath` under Caches/).
public struct MessageBodyRecord: Codable, Sendable, Hashable, FetchableRecord, PersistableRecord {
    public static let databaseTableName = "message_body"
    public var messageId: UUID
    public var htmlPath: String?
    public var text: String?
    public var remoteImageCount: Int
    public var byteSize: Int
    public var fetchedAt: Date
    public var lastAccessedAt: Date
    public init(messageId: UUID, htmlPath: String?, text: String?, remoteImageCount: Int, byteSize: Int,
                fetchedAt: Date = .now, lastAccessedAt: Date = .now) {
        self.messageId = messageId; self.htmlPath = htmlPath; self.text = text; self.remoteImageCount = remoteImageCount
        self.byteSize = byteSize; self.fetchedAt = fetchedAt; self.lastAccessedAt = lastAccessedAt
    }
}

public struct ThreadItemRecord: Codable, Sendable, Hashable, FetchableRecord, PersistableRecord {
    public static let databaseTableName = "thread_item"
    public var threadKey: String
    public var messageId: UUID
    public var position: Int
    public init(threadKey: String, messageId: UUID, position: Int) {
        self.threadKey = threadKey; self.messageId = messageId; self.position = position
    }
}

public struct PrefRecord: Codable, Sendable, Hashable, FetchableRecord, PersistableRecord {
    public static let databaseTableName = "pref"
    public var key: String
    public var value: String
    public init(key: String, value: String) { self.key = key; self.value = value }
}

public struct RecentSearchRecord: Codable, Sendable, Hashable, FetchableRecord, PersistableRecord {
    public static let databaseTableName = "recent_search"
    public var q: String
    public var searchedAt: Date
    public init(q: String, searchedAt: Date = .now) { self.q = q; self.searchedAt = searchedAt }
}

/// Outbox row: one message id per row; `op` is a server batch op (read|unread|archive|unarchive).
public struct PendingOp: Codable, Sendable, Hashable, Identifiable, FetchableRecord, MutablePersistableRecord {
    public static let databaseTableName = "pending_op"
    public var id: Int64?
    public var kind: String
    public var messageId: UUID
    public var idempotencyKey: String
    /// Set once a flush has grouped the row into a request; such rows are no longer cancellable.
    public var inFlight: Bool
    public var attempts: Int
    public var lastError: String?
    public var createdAt: Date
    public init(id: Int64? = nil, kind: String, messageId: UUID, idempotencyKey: String = UUID().uuidString,
                inFlight: Bool = false, attempts: Int = 0, lastError: String? = nil, createdAt: Date = .now) {
        self.id = id; self.kind = kind; self.messageId = messageId; self.idempotencyKey = idempotencyKey
        self.inFlight = inFlight; self.attempts = attempts; self.lastError = lastError; self.createdAt = createdAt
    }
    public mutating func didInsert(_ inserted: InsertionSuccess) { id = inserted.rowID }
}

/// Cached rule (list fields). `position` is the priority: lowest wins.
public struct RuleRecord: Codable, Sendable, Hashable, Identifiable, FetchableRecord, PersistableRecord {
    public static let databaseTableName = "rule"
    public var id: UUID
    public var name: String
    public var position: Int
    public var enabled: Bool
    public var businessId: UUID?
    public var matchToLocal: String?
    public var matchFrom: String?
    public var matchSubject: String?
    public var gmailLabel: String?
    public var gmailArchive: Bool
    public var gmailMarkRead: Bool
    public var labelOnly: Bool
    public var importedFromFilter: String?
    public var messageCount: Int
    public init(id: UUID, name: String, position: Int, enabled: Bool = true, businessId: UUID? = nil, matchToLocal: String? = nil,
                matchFrom: String? = nil, matchSubject: String? = nil, gmailLabel: String? = nil, gmailArchive: Bool = false,
                gmailMarkRead: Bool = false, labelOnly: Bool = true, importedFromFilter: String? = nil, messageCount: Int = 0) {
        self.id = id; self.name = name; self.position = position; self.enabled = enabled; self.businessId = businessId
        self.matchToLocal = matchToLocal; self.matchFrom = matchFrom; self.matchSubject = matchSubject
        self.gmailLabel = gmailLabel; self.gmailArchive = gmailArchive; self.gmailMarkRead = gmailMarkRead
        self.labelOnly = labelOnly; self.importedFromFilter = importedFromFilter; self.messageCount = messageCount
    }
}
