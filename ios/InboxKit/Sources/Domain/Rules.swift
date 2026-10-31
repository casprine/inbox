import Foundation
import API
import Persistence

extension RuleRecord {
    public init(_ r: API.Rule) {
        self.init(id: r.id, name: r.name, position: r.position, enabled: r.enabled, businessId: r.businessId,
                  matchToLocal: r.matchToLocal, matchFrom: r.matchFrom, matchSubject: r.matchSubject, gmailLabel: r.gmailLabel,
                  gmailArchive: r.gmailArchive, gmailMarkRead: r.gmailMarkRead, labelOnly: r.labelOnly,
                  importedFromFilter: r.importedFromFilter, messageCount: r.messageCount)
    }

    /// One-line description for the list, as the board words it: "invoices@ on acme-studio.com",
    /// "From @stripe.com on kettle.house", "Any other address on acme-studio.com".
    public func matchSummary(businessName: String?, domain: String? = nil) -> String {
        var parts: [String] = []
        if let t = matchToLocal, !t.isEmpty { parts.append("\(t)@") }
        if let f = matchFrom, !f.isEmpty { parts.append("From \(f)") }
        if let s = matchSubject, !s.isEmpty { parts.append("Subject matches \(s)") }
        let on = domain ?? businessName
        if parts.isEmpty { return on.map { "Any other address on \($0)" } ?? "Any mail" }
        let head = parts.joined(separator: " · ")
        return on.map { "\(head) on \($0)" } ?? head
    }
}

/// Editable rule: strings are what the user typed; `input` trims and turns empty conditions into nil.
public struct RuleDraft: Hashable, Sendable {
    public var id: UUID?
    public var name = ""
    public var enabled = true
    public var businessId: UUID?
    public var toLocal = ""
    public var from = ""
    public var subject = ""
    public var label = ""
    public var archive = false
    public var markRead = false
    public var labelOnly = true

    public init() {}

    public init(rule r: RuleRecord) {
        id = r.id; name = r.name; enabled = r.enabled; businessId = r.businessId
        toLocal = r.matchToLocal ?? ""; from = r.matchFrom ?? ""; subject = r.matchSubject ?? ""
        label = r.gmailLabel ?? ""; archive = r.gmailArchive; markRead = r.gmailMarkRead; labelOnly = r.labelOnly
    }

    private static func clean(_ s: String) -> String? {
        let t = s.trimmingCharacters(in: .whitespacesAndNewlines)
        return t.isEmpty ? nil : t
    }

    public var input: RuleInput {
        RuleInput(id: id, name: name.trimmingCharacters(in: .whitespacesAndNewlines), enabled: enabled, businessId: businessId,
                  matchToLocal: Self.clean(toLocal), matchFrom: Self.clean(from), matchSubject: Self.clean(subject),
                  gmailLabel: Self.clean(label), gmailArchive: archive, gmailMarkRead: markRead, labelOnly: labelOnly)
    }

    public var hasName: Bool { Self.clean(name) != nil }

    // MARK: Rule from message

    static let stopWords: Set<String> = ["re", "fwd", "fw", "the", "your", "you", "our", "for", "and", "with", "from", "this",
                                         "that", "have", "has", "new", "about", "into", "are", "was", "will", "not"]

    /// First distinctive word of a subject (prefixes like "Re:" and short or common words skipped), lowercased.
    public static func keyword(fromSubject subject: String?) -> String? {
        let words = (subject ?? "").lowercased().split { !$0.isLetter && !$0.isNumber }.map(String.init)
        return words.first { $0.count >= 4 && !stopWords.contains($0) && !$0.allSatisfy(\.isNumber) }
    }

    /// Prefill for "Rule from this message": subject contains a keyword, the message's business, a
    /// "<Business>/<Name>" label and archive off (label only, so nothing leaves the inbox until the user opts in).
    public static func fromMessage(_ m: API.Message, businessName: String?) -> RuleDraft {
        var d = RuleDraft()
        d.businessId = m.businessId
        if let k = keyword(fromSubject: m.subject) {
            d.name = k.prefix(1).uppercased() + k.dropFirst()
            d.subject = NSRegularExpression.escapedPattern(for: k)
        } else if let from = m.fromName ?? m.fromAddr {
            d.name = from
            d.from = m.fromAddr ?? ""
        }
        if let b = businessName?.split(separator: " ").first, d.hasName { d.label = "\(b)/\(d.name)" }
        return d
    }

    /// Human form of the subject condition ("Subject contains “wholesale”") when the pattern is one plain word.
    public var subjectDescription: String? {
        guard let s = Self.clean(subject) else { return nil }
        let plain = s.allSatisfy { $0.isLetter || $0.isNumber || $0 == " " }
        return plain ? "Subject contains “\(s)”" : "Subject matches \(s)"
    }
}

/// Where Gmail labels come from. The real `/v1/gmail/labels` client lands with T20; until then the app uses a stub.
public protocol LabelsProviding: Sendable {
    func labels() async throws -> [String]
    func create(_ name: String) async throws -> String
}

/// Real `/v1/gmail/labels` client. If the server doesn't have the endpoint yet (404/501) the stub list keeps the picker usable.
public actor APILabelsProvider: LabelsProviding {
    private let api: APIClient
    private let fallback = StubLabelsProvider()
    public init(api: APIClient) { self.api = api }

    private func missing(_ e: Error) -> Bool {
        guard let e = e as? APIError else { return false }
        switch e {
        case .http(let s): return s == 404 || s == 501
        case .problem(let p, _): return p.status == 404 || p.status == 501
        default: return false
        }
    }
    public func labels() async throws -> [String] {
        do { return try await api.gmailLabels().map(\.name).sorted { $0.localizedCaseInsensitiveCompare($1) == .orderedAscending } }
        catch { if missing(error) { return try await fallback.labels() }; throw error }
    }
    public func create(_ name: String) async throws -> String {
        do { return try await api.createGmailLabel(name).name }
        catch { if missing(error) { return try await fallback.create(name) }; throw error }
    }
}

public actor StubLabelsProvider: LabelsProviding {
    private var names: [String]
    public init(_ names: [String] = ["Acme/Invoices", "Acme/Press", "Kettle/Wholesale", "Kettle/Orders", "Northgate/Accounts", "Receipts", "Reading"]) {
        self.names = names
    }
    public func labels() async throws -> [String] { names }
    public func create(_ name: String) async throws -> String {
        let n = name.trimmingCharacters(in: .whitespacesAndNewlines)
        if !names.contains(n) { names.append(n) }
        return n
    }
}

/// Rules: cache first (stale-while-revalidate), optimistic switch + reorder with rollback.
public actor RulesRepository {
    private let api: APIClient
    private let db: AppDatabase
    public init(api: APIClient, db: AppDatabase) { self.api = api; self.db = db }

    public nonisolated func observe() -> AsyncStream<[RuleRecord]> { db.observeRules() }

    public func refresh() async throws {
        try await db.replaceRules(try await api.rules().map(RuleRecord.init))
    }

    /// Flips the switch locally first; if the server refuses, the old value is restored and the error rethrown.
    public func setEnabled(_ enabled: Bool, id: UUID) async throws {
        try await db.setRuleEnabled(id: id, enabled)
        do { _ = try await api.patchRule(id: id, RulePatch(enabled: enabled)) }
        catch { try? await db.setRuleEnabled(id: id, !enabled); throw error }
    }

    /// Creates (no id) or edits a rule. Throws the `APIError` (422 carries `ruleFieldErrors`).
    @discardableResult
    public func save(_ draft: RuleDraft) async throws -> RuleRecord {
        let saved: API.Rule
        if let id = draft.id { saved = try await api.patchRule(id: id, draft.input) }
        else { saved = try await api.createRule(draft.input) }
        let rec = RuleRecord(saved)
        try await db.saveRule(rec)
        try? await refresh()   // a new rule lands at the top, which shifts the others
        return rec
    }

    public func delete(id: UUID) async throws {
        let before = try await db.rules()
        try await db.deleteRule(id: id)
        do { try await api.deleteRule(id: id) }
        catch { try? await db.replaceRules(before); throw error }
    }

    /// Applies the new order locally, sends `PUT /rules:order`, and restores the old order on failure.
    public func move(ids: [UUID]) async throws {
        let before = try await db.rules()
        try await db.reorderRules(ids: ids)
        do { try await db.replaceRules(try await api.orderRules(ids: ids).map(RuleRecord.init)) }
        catch { try? await db.replaceRules(before); throw error }
    }

    public func test(_ draft: RuleDraft, scope: RuleTestScope = .all, limit: Int = 200) async throws -> RuleTestResult {
        try await api.testRule(draft.input, scope: scope, limit: limit)
    }
}
