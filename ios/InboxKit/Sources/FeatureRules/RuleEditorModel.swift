import Foundation
import Observation
import API
import Domain
import Persistence

@MainActor @Observable
public final class RuleEditorModel {
    public var draft: RuleDraft
    public private(set) var businesses: [BusinessRecord] = []
    /// Server (or local) errors keyed by field: `match_subject`, `match_from`, `match_to_local`, `name`, `gmail_label`.
    public private(set) var fieldErrors: [String: String] = [:]
    public private(set) var generalError: String?
    public private(set) var isSaving = false
    private let env: AppEnvironment

    public init(env: AppEnvironment, draft: RuleDraft) { self.env = env; self.draft = draft }

    public var isNew: Bool { draft.id == nil }
    public var title: String { draft.hasName ? draft.name : (isNew ? "New rule" : "Rule") }
    public var businessName: String? { businesses.first { $0.id == draft.businessId }?.name }

    public func load() async { businesses = (try? await env.database.businesses()) ?? [] }

    /// Local check of the subject pattern so Save can be disabled with a reason before a round trip (RU07).
    /// The server stays the authority (RE2): its 422 text replaces this once it answers.
    public nonisolated static func patternProblem(_ pattern: String) -> String? {
        let t = pattern.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !t.isEmpty else { return nil }
        if t.count > 500 { return "Pattern is too long (500 characters at most)." }
        var depth = 0
        var prev: Character?
        for ch in t {
            if prev == "\\" { prev = nil; continue }
            if ch == "(" { depth += 1 } else if ch == ")" { depth -= 1; if depth < 0 { return "Pattern isn't valid: unexpected )." } }
            prev = ch
        }
        if depth > 0 { return "Missing a closing bracket “)”." }
        if (try? NSRegularExpression(pattern: t)) == nil { return "Pattern isn't valid. Check brackets and escapes." }
        return nil
    }

    public var localSubjectError: String? { Self.patternProblem(draft.subject) }

    /// Why Save is off, in plain words; nil when it's allowed.
    public var saveBlockedReason: String? {
        if !draft.hasName { return "Give the rule a name." }
        if let e = localSubjectError ?? fieldErrors["match_subject"] { return e }
        return nil
    }
    public var canSave: Bool { saveBlockedReason == nil && !isSaving }

    public func subjectError() -> String? { localSubjectError ?? fieldErrors["match_subject"] }

    public func edited() { fieldErrors = [:]; generalError = nil }

    @discardableResult
    public func save() async -> Bool {
        guard canSave else { return false }
        isSaving = true; defer { isSaving = false }
        do { try await env.rules.save(draft); return true }
        catch let e as APIError { apply(e); return false }
        catch { generalError = "Couldn't save the rule. Try again."; return false }
    }

    /// Absorbs a 422 into field errors; anything else becomes a general message.
    public func apply(_ e: APIError) {
        let errs = e.ruleFieldErrors
        if errs.isEmpty { generalError = e.isRetryable ? "Couldn't reach the server. Try again." : "Couldn't save the rule." ; return }
        fieldErrors = Dictionary(errs.map { ($0.field, $0.message) }, uniquingKeysWith: { a, _ in a })
    }
}
