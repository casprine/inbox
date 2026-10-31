import Foundation
import Observation
import API
import Domain
import Persistence

/// Rules tab list: cached rules (SWR), optimistic switch, reorder by drag, delete with confirmation.
@MainActor @Observable
public final class RulesListModel {
    public private(set) var rules: [RuleRecord] = []
    public private(set) var businessNames: [UUID: String] = [:]
    public private(set) var businessDomains: [UUID: String] = [:]
    public var query = ""
    public private(set) var hasLoaded = false
    public private(set) var refreshFailed = false
    public private(set) var notice: String?
    public var pendingDelete: RuleRecord?
    private let env: AppEnvironment

    public init(env: AppEnvironment) { self.env = env }

    public var isLoading: Bool { !hasLoaded && rules.isEmpty }
    public var isEmpty: Bool { hasLoaded && rules.isEmpty }

    public func summary(_ r: RuleRecord) -> String {
        r.matchSummary(businessName: r.businessId.flatMap { businessNames[$0] }, domain: r.businessId.flatMap { businessDomains[$0] })
    }
    /// Rules shown in the list: all of them, or those whose name or description contains the search text.
    public var filtered: [RuleRecord] {
        let q = query.trimmingCharacters(in: .whitespaces)
        return q.isEmpty ? rules : rules.filter { $0.name.localizedCaseInsensitiveContains(q) || summary($0).localizedCaseInsensitiveContains(q) }
    }

    public func observe() async {
        await loadBusinesses()
        if let cached = try? await env.database.rules() { rules = cached; if !cached.isEmpty { hasLoaded = true } }
        for await list in env.rules.observe() { rules = list; if !list.isEmpty { hasLoaded = true } }
    }

    func loadBusinesses() async {
        let list = (try? await env.database.businesses()) ?? []
        businessNames = Dictionary(uniqueKeysWithValues: list.map { ($0.id, $0.name) })
        businessDomains = Dictionary(uniqueKeysWithValues: list.map { ($0.id, $0.domain) })
    }

    public func refresh() async {
        do { try await env.rules.refresh(); refreshFailed = false } catch { refreshFailed = true }
        hasLoaded = true
    }

    public func setEnabled(_ on: Bool, for rule: RuleRecord) async {
        do { try await env.rules.setEnabled(on, id: rule.id); notice = nil }
        catch { notice = "Couldn't change “\(rule.name)”. It's back to how it was." }
    }

    /// New id order after SwiftUI's `onMove`.
    public nonisolated static func reordered(_ ids: [UUID], from: IndexSet, to: Int) -> [UUID] {
        var out = ids
        let moving = from.sorted().map { out[$0] }
        for i in from.sorted().reversed() { out.remove(at: i) }
        let shift = from.filter { $0 < to }.count
        out.insert(contentsOf: moving, at: min(max(to - shift, 0), out.count))
        return out
    }

    public func move(from: IndexSet, to: Int) async {
        let ids = Self.reordered(rules.map(\.id), from: from, to: to)
        do { try await env.rules.move(ids: ids); notice = nil }
        catch { notice = "Couldn't save the new order. It's back to how it was." }
    }

    public func confirmDelete() async {
        guard let r = pendingDelete else { return }
        pendingDelete = nil
        do { try await env.rules.delete(id: r.id); notice = nil }
        catch { notice = "Couldn't delete “\(r.name)”." }
    }
}
