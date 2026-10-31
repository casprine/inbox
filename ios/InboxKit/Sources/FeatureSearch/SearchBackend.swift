import Foundation
import API
import Persistence

/// One matching message, display-ready. Spans are unicode-scalar offsets into `subject`, `sender` and `snippet`.
public struct SearchHit: Identifiable, Sendable, Hashable {
    public var id: UUID
    public var sender: String
    public var subject: String
    public var snippet: String
    public var receivedAt: Date
    public var isRead: Bool
    public var attachmentCount: Int
    public var senderSpans: [Span]
    public var subjectSpans: [Span]
    public var snippetSpans: [Span]
}

public struct SearchSection: Identifiable, Sendable, Hashable {
    public var id: String
    public var name: String
    public var total: Int
    public var hits: [SearchHit]
}

public struct SearchOutcome: Sendable {
    public var sections: [SearchSection]
    public var terms: [String]
    /// Cursor for the next (older) page; nil on the last page.
    public var nextCursor: String?
    public var hitCount: Int { sections.reduce(0) { $0 + $1.hits.count } }
    public init(sections: [SearchSection], terms: [String], nextCursor: String? = nil) { self.sections = sections; self.terms = terms; self.nextCursor = nextCursor }
}

public protocol SearchBackend: Sendable {
    func search(_ q: String, business: UUID?, cursor: String?) async throws -> SearchOutcome
    func recents() async -> [String]
    func addRecent(_ q: String) async -> [String]
    func removeRecent(_ q: String) async -> [String]
    func clearRecents() async
}

public extension SearchBackend {
    func search(_ q: String, business: UUID?) async throws -> SearchOutcome { try await search(q, business: business, cursor: nil) }
}

enum RecentList {
    static let limit = 10
    static func adding(_ q: String, to list: [String]) -> [String] {
        Array(([q] + list.filter { $0.caseInsensitiveCompare(q) != .orderedSame }).prefix(limit))
    }
}

/// Server search. Recents go to the server and are mirrored into the local cache so the idle screen works offline.
public struct APISearchBackend: SearchBackend {
    let api: APIClient
    let db: AppDatabase
    public init(api: APIClient, db: AppDatabase) { self.api = api; self.db = db }

    /// First page 50 hits; `cursor` fetches the next older page.
    public func search(_ q: String, business: UUID?, cursor: String?) async throws -> SearchOutcome {
        do {
            let r = try await api.search(q, business: business, limit: 50, cursor: cursor)
            return Self.outcome(r)
        } catch let e as APIError where e.code == "empty_query" {
            return SearchOutcome(sections: [], terms: [])
        }
    }

    public func recents() async -> [String] {
        if let r = try? await api.recentSearches() { try? await db.replaceRecentSearches(r); return r }
        return (try? await db.recentSearches()) ?? []
    }

    public func addRecent(_ q: String) async -> [String] {
        let local = RecentList.adding(q, to: (try? await db.recentSearches()) ?? [])
        try? await db.replaceRecentSearches(local)
        if let r = try? await api.addRecentSearch(q) { try? await db.replaceRecentSearches(r); return r }
        return local
    }

    public func removeRecent(_ q: String) async -> [String] {
        try? await api.deleteRecentSearches(q: q)
        let local = ((try? await db.recentSearches()) ?? []).filter { $0 != q }
        try? await db.replaceRecentSearches(local)
        return local
    }

    public func clearRecents() async {
        try? await api.deleteRecentSearches()
        try? await db.replaceRecentSearches([])
    }

    static func outcome(_ r: SearchResponse) -> SearchOutcome {
        let sections = r.groups.map { g in
            SearchSection(id: g.business?.id.uuidString ?? "none", name: g.business?.name ?? "Unrouted", total: g.total,
                          hits: g.messages.map(hit))
        }
        return SearchOutcome(sections: sections, terms: r.parsed.terms, nextCursor: r.hasMore ? r.nextCursor : nil)
    }

    static func hit(_ m: SearchResult) -> SearchHit {
        let name = m.fromName ?? "", addr = m.fromAddr ?? ""
        // Server "from" offsets index "<name> <addr>"; we show the name when there is one.
        let nameLen = name.unicodeScalars.count
        let sender: String, senderSpans: [Span]
        if !name.isEmpty {
            sender = name
            senderSpans = m.highlights.from.filter { $0.start < nameLen }.map { Span(start: $0.start, end: min($0.end, nameLen)) }
        } else { sender = addr.isEmpty ? "Unknown sender" : addr; senderSpans = addr.isEmpty ? [] : m.highlights.from }
        let subject = m.subject?.isEmpty == false ? m.subject! : "(no subject)"
        return SearchHit(id: m.id, sender: sender, subject: subject, snippet: m.snippet ?? "", receivedAt: m.receivedAt, isRead: m.isRead,
                         attachmentCount: m.attachmentCount, senderSpans: senderSpans,
                         subjectSpans: m.subject?.isEmpty == false ? m.highlights.subject : [], snippetSpans: m.highlights.snippet)
    }
}

/// Mock mode: searches the seeded local database with the client-side query reader.
public struct LocalSearchBackend: SearchBackend {
    let db: AppDatabase
    public init(db: AppDatabase) { self.db = db }

    public func search(_ raw: String, business: UUID?, cursor: String?) async throws -> SearchOutcome {
        let q = SearchQuery(raw)
        guard !q.isEmpty else { return SearchOutcome(sections: [], terms: []) }
        let businesses = try await db.businesses().sorted { $0.sortOrder < $1.sortOrder }
        let rows = try await db.messages().filter { m in
            (business == nil || m.businessId == business) && Self.matches(m, q)
        }.sorted { $0.receivedAt > $1.receivedAt }
        var sections: [SearchSection] = []
        for b in businesses {
            let hits = rows.filter { $0.businessId == b.id }.map { Self.hit($0, q) }
            if !hits.isEmpty { sections.append(SearchSection(id: b.id.uuidString, name: b.name, total: hits.count, hits: hits)) }
        }
        let loose = rows.filter { r in !businesses.contains { $0.id == r.businessId } }.map { Self.hit($0, q) }
        if !loose.isEmpty { sections.append(SearchSection(id: "none", name: "Unrouted", total: loose.count, hits: loose)) }
        return SearchOutcome(sections: sections, terms: q.terms)
    }

    static func matches(_ m: MessageRecord, _ q: SearchQuery) -> Bool {
        let sender = "\(m.fromName ?? "") \(m.fromAddr ?? "")".lowercased()
        let text = "\(m.subject ?? "") \(m.snippet ?? "") \(m.fromName ?? "") \(m.fromAddr ?? "")".lowercased()
        let tokens = text.split(whereSeparator: { !$0.isLetter && !$0.isNumber }).map(String.init)
        if q.hasAttachment && m.attachmentCount == 0 { return false }
        if !q.from.allSatisfy({ sender.contains($0.lowercased()) }) { return false }
        if !q.to.allSatisfy({ t in "\(m.toLocal ?? "")+\(m.toTag ?? "")".lowercased().contains(t.lowercased()) }) { return false }
        if !q.phrases.allSatisfy({ text.contains($0.lowercased()) }) { return false }
        return q.words.allSatisfy { w in tokens.contains { $0.hasPrefix(w) } }
    }

    static func hit(_ m: MessageRecord, _ q: SearchQuery) -> SearchHit {
        let sender = [m.fromName, m.fromAddr].compactMap { $0 }.first { !$0.isEmpty } ?? "Unknown sender"
        let subject = m.subject?.isEmpty == false ? m.subject! : "(no subject)"
        let t = q.terms + q.from
        return SearchHit(id: m.id, sender: sender, subject: subject, snippet: m.snippet ?? "", receivedAt: m.receivedAt, isRead: m.isRead,
                         attachmentCount: m.attachmentCount, senderSpans: SearchHighlight.spans(in: sender, terms: t),
                         subjectSpans: SearchHighlight.spans(in: subject, terms: q.terms), snippetSpans: SearchHighlight.spans(in: m.snippet ?? "", terms: q.terms))
    }

    public func recents() async -> [String] { (try? await db.recentSearches()) ?? [] }
    public func addRecent(_ q: String) async -> [String] {
        let l = RecentList.adding(q, to: await recents()); try? await db.replaceRecentSearches(l); return l
    }
    public func removeRecent(_ q: String) async -> [String] {
        let l = await recents().filter { $0 != q }; try? await db.replaceRecentSearches(l); return l
    }
    public func clearRecents() async { try? await db.replaceRecentSearches([]) }
}
