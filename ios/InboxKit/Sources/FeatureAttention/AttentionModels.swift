import Foundation
import Observation
import API
import Domain
import Persistence

/// Unrouted (NA02): messages no rule matched, grouped by business.
@MainActor @Observable
public final class UnroutedModel {
    public private(set) var groups: [UnroutedList.Group] = []
    public private(set) var total = 0
    public private(set) var isLoading = true
    public private(set) var failed = false
    private let env: AppEnvironment
    public init(env: AppEnvironment) { self.env = env }

    public var isEmpty: Bool { !isLoading && total == 0 }

    public func load() async {
        do {
            let l = try await env.api.unrouted()
            groups = l.groups; total = l.total; failed = false
            try? await env.database.setPref("attention.unrouted", String(l.total))
        } catch { failed = true }
        isLoading = false
    }

    public func businessName(_ g: UnroutedList.Group) -> String { g.business?.name ?? "No business" }
}

/// Rule from message (NA03): a prefilled draft plus how many unrouted messages it would catch.
@MainActor @Observable
public final class RuleFromMessageModel {
    public var draft: RuleDraft
    public private(set) var matches: Int?
    public private(set) var unroutedTotal: Int
    public private(set) var businessNames: [UUID: String] = [:]
    public private(set) var errorText: String?
    public private(set) var isSaving = false
    private let env: AppEnvironment
    private var task: Task<Void, Never>?

    public init(env: AppEnvironment, message: API.Message, businessName: String?, unroutedTotal: Int) {
        self.env = env; self.unroutedTotal = unroutedTotal
        draft = RuleDraft.fromMessage(message, businessName: businessName)
    }

    public var canCreate: Bool { draft.hasName && RuleEditorModelPattern.problem(draft.subject) == nil && !isSaving }
    public var matchSummary: String {
        guard let m = matches else { return "Checking unrouted mail…" }
        return "Matches \(m) of the \(unroutedTotal) unrouted message\(unroutedTotal == 1 ? "" : "s")."
    }

    public func load() async {
        businessNames = Dictionary(uniqueKeysWithValues: ((try? await env.database.businesses()) ?? []).map { ($0.id, $0.name) })
        await recount()
    }

    /// Re-run the unrouted test after the draft changes (debounced by the caller via task(id:)).
    public func recount() async {
        guard RuleEditorModelPattern.problem(draft.subject) == nil else { matches = nil; return }
        do { matches = try await env.rules.test(draft, scope: .unrouted).wouldMatch.count } catch { matches = nil }
    }

    /// Creates the rule and applies it to unrouted mail so the messages leave this list.
    public func create() async -> Bool {
        guard canCreate else { return false }
        isSaving = true; defer { isSaving = false }
        do {
            let rec = try await env.rules.save(draft)
            _ = try? await env.api.applyRule(id: rec.id, scope: .unrouted)
            return true
        } catch let e as APIError {
            errorText = e.ruleFieldErrors.first?.message ?? (e.isRetryable ? "Couldn't reach the server. Try again." : "Couldn't create the rule.")
            return false
        } catch { errorText = "Couldn't create the rule."; return false }
    }
}

/// Failed (NA04/NA07): items that couldn't be stored, with retry per row.
@MainActor @Observable
public final class FailedModel {
    public private(set) var items: [FailedMessage] = []
    public private(set) var isLoading = true
    public private(set) var failed = false
    public private(set) var retrying: Set<UUID> = []
    public private(set) var notice: String?
    public private(set) var businessNames: [UUID: String] = [:]
    private let env: AppEnvironment
    public init(env: AppEnvironment) { self.env = env }

    public var isEmpty: Bool { !isLoading && items.isEmpty }

    /// DD-9: the Failed list is grouped by business like Unrouted (first appearance order; mail without a business last).
    public struct Group: Identifiable { public let businessId: UUID?; public let name: String; public let items: [FailedMessage]; public var id: String { businessId?.uuidString ?? "none" } }
    public var groups: [Group] {
        var order: [UUID?] = []
        for f in items where !order.contains(where: { $0 == f.message.businessId }) { order.append(f.message.businessId) }
        let sorted = order.filter { $0 != nil } + order.filter { $0 == nil }
        return sorted.map { id in
            Group(businessId: id, name: id.flatMap { businessNames[$0] } ?? "No business", items: items.filter { $0.message.businessId == id })
        }
    }

    public func load() async {
        if let rows = try? await env.database.businesses() { businessNames = Dictionary(rows.map { ($0.id, $0.name) }, uniquingKeysWith: { a, _ in a }) }
        do { items = try await env.api.failed(); failed = false; try? await env.database.setPref("attention.failed", String(items.count)) }
        catch { failed = true }
        isLoading = false
    }

    /// Queue a retry; rows stay in the list as "Retrying…" until the server finishes, then a reload shows the result.
    public func retry(_ id: UUID) async {
        retrying.insert(id)
        do { _ = try await env.api.retryFailed(id: id); notice = nil }
        catch let e as APIError {
            notice = isNeedsGmail(e) ? "This one needs Gmail connected to fetch the original." : "Couldn't retry. Try again."
        } catch { notice = "Couldn't retry. Try again." }
        try? await Task.sleep(for: .milliseconds(600))
        await load()
        retrying.remove(id)
    }

    /// Snapshot aid (NA06): shows the "Retrying…" row without waiting for a real retry.
    public func previewRetrying(_ id: UUID) { retrying.insert(id) }

    public func dismiss(_ id: UUID) async -> Bool {
        do {
            try await env.api.dismissFailed(id: id)
            items.removeAll { $0.id == id }
            try? await env.database.setPref("attention.failed", String(items.count))
            return true
        } catch { notice = "Couldn't dismiss. Try again."; return false }
    }

    private func isNeedsGmail(_ e: APIError) -> Bool {
        if case .problem(let p, _) = e { return p.code == "needs_gmail" }
        return false
    }
}

public enum FailureCopy {
    /// Bold lead + explanation for a failure reason (D2 spec §3).
    public static func text(reason: String?, sizeBytes: Int?, delivered: Bool = true) -> (lead: String, detail: String) {
        let tail = delivered ? " It was still delivered to Gmail." : ""
        switch reason {
        case "parse_failed": return ("Couldn't read this message.", "It arrived, but its format couldn't be understood.\(tail)")
        case "too_large": return ("Too large to store.", "It's over the size limit.\(tail)")
        default: return ("Couldn't store.", "The server couldn't save this message.\(tail)")
        }
    }
}

/// Shared with the editor's validation without importing FeatureRules' model type everywhere.
enum RuleEditorModelPattern {
    static func problem(_ s: String) -> String? {
        let t = s.trimmingCharacters(in: .whitespacesAndNewlines)
        return t.isEmpty || (try? NSRegularExpression(pattern: t)) != nil ? nil : "invalid"
    }
}
