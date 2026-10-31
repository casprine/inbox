import Foundation
import Observation
import API

public enum SearchOperator: String, CaseIterable, Sendable, Identifiable {
    case from = "from:", to = "to:", hasAttachment = "has:attachment"
    public var id: String { rawValue }
    public var hint: String {
        switch self {
        case .from: "Sender name or address"
        case .to: "One of your addresses, like invoices@"
        case .hasAttachment: "Only mail with files attached"
        }
    }
}

/// Drives the Search screen: debounced live search, in-flight cancellation, recents, scope.
@MainActor @Observable
public final class SearchModel {
    public enum Phase: Equatable { case idle, results, none, failed }

    public var query: String = "" { didSet { if query != oldValue { queryChanged() } } }
    public private(set) var phase: Phase = .idle
    public private(set) var sections: [SearchSection] = []
    public private(set) var terms: [String] = []
    public private(set) var nextCursor: String?
    public private(set) var isLoadingMore = false
    public private(set) var loadMoreFailed = false
    public private(set) var recents: [String] = []
    public private(set) var isSearching = false
    public private(set) var scope: UUID?
    public private(set) var scopeName: String?
    /// The business the search was opened from; stays set when the user widens to all (drives the segmented control).
    public private(set) var contextScope: UUID?
    public private(set) var contextName: String?
    /// True while an older-mail import runs; the no-results state then explains results may be incomplete.
    public var importing: Bool

    private let backend: any SearchBackend
    private let debounce: Duration
    private var task: Task<Void, Never>?
    private var generation = 0

    public init(backend: any SearchBackend, scope: UUID? = nil, scopeName: String? = nil, importing: Bool = false,
                debounce: Duration = .milliseconds(250)) {
        self.backend = backend; self.scope = scope; contextScope = scope; contextName = scopeName; self.scopeName = scopeName; self.importing = importing; self.debounce = debounce
    }

    public var trimmed: String { query.trimmingCharacters(in: .whitespacesAndNewlines) }
    public var resultCount: Int { sections.reduce(0) { $0 + $1.hits.count } }
    public var showsScopeChip: Bool { scope != nil }

    public var showsScopeControl: Bool { contextScope != nil }
    public func setScopeName(_ name: String?) { scopeName = name; contextName = name }
    public func loadRecents() async { recents = await backend.recents() }

    /// Resolves once the current debounce/search cycle has finished (tests, submit).
    public func settle() async { await task?.value }

    // MARK: Query changes

    private func queryChanged() {
        task?.cancel()
        generation += 1
        guard !trimmed.isEmpty else { reset(); return }
        run(after: debounce)
    }

    private func reset() { task = nil; isSearching = false; phase = .idle; sections = []; terms = []; nextCursor = nil; isLoadingMore = false; loadMoreFailed = false }

    public var canLoadMore: Bool { nextCursor != nil && phase == .results }

    private func run(after delay: Duration?) {
        let q = trimmed, scope = scope, gen = generation
        isSearching = true
        task = Task { [backend] in
            do {
                if let delay { try await Task.sleep(for: delay) }
                let r = try await backend.search(q, business: scope)
                try Task.checkCancellation()
                guard gen == self.generation else { return }
                self.sections = r.sections; self.terms = r.terms; self.nextCursor = r.nextCursor
                self.isLoadingMore = false; self.loadMoreFailed = false
                self.phase = r.hitCount == 0 ? .none : .results
                self.isSearching = false
            } catch is CancellationError {
                return
            } catch {
                guard !Task.isCancelled, gen == self.generation else { return }
                self.isSearching = false
                self.phase = .failed
            }
        }
    }

    /// Fetches the next older page and merges its groups into the existing sections (by business).
    public func loadMore() async {
        guard let cursor = nextCursor, !isLoadingMore, phase == .results else { return }
        let q = trimmed, scope = scope, gen = generation
        isLoadingMore = true; loadMoreFailed = false
        do {
            let r = try await backend.search(q, business: scope, cursor: cursor)
            guard gen == generation else { return }
            for s in r.sections {
                if let i = sections.firstIndex(where: { $0.id == s.id }) {
                    let known = Set(sections[i].hits.map(\.id))
                    sections[i].hits += s.hits.filter { !known.contains($0.id) }
                    sections[i].total += s.total
                } else { sections.append(s) }
            }
            nextCursor = r.nextCursor
        } catch {
            guard gen == generation else { return }
            loadMoreFailed = true
        }
        isLoadingMore = false
    }

    /// Search now (keyboard Search key / tapping a recent) and remember the query.
    public func submit() async {
        guard !trimmed.isEmpty else { return }
        task?.cancel(); generation += 1
        run(after: nil)
        recents = await backend.addRecent(trimmed)
        await settle()
    }

    public func useRecent(_ q: String) async { query = q; await submit() }

    /// Called when a result is opened: the query was useful, so keep it.
    public func opened() async { if !trimmed.isEmpty { recents = await backend.addRecent(trimmed) } }

    // MARK: Operators, recents, scope

    /// Appends an operator token to the query (with a separating space) and returns the new text.
    @discardableResult
    public func insert(_ op: SearchOperator) -> String {
        var q = query
        if !q.isEmpty && !q.hasSuffix(" ") { q += " " }
        query = q + op.rawValue + (op == .hasAttachment ? " " : "")
        return query
    }

    public func clearRecents() async { recents = []; await backend.clearRecents() }
    public func removeRecent(_ q: String) async { recents = await backend.removeRecent(q) }

    /// Scope chip tapped: search every business.
    public func widenScope() {
        guard scope != nil else { return }
        scope = nil; scopeName = nil
        guard !trimmed.isEmpty else { return }
        task?.cancel(); generation += 1
        run(after: nil)
    }

    /// Segmented control back to the original business.
    public func narrowScope() {
        guard scope == nil, let c = contextScope else { return }
        scope = c; scopeName = contextName
        guard !trimmed.isEmpty else { return }
        task?.cancel(); generation += 1
        run(after: nil)
    }

    public func retry() { guard !trimmed.isEmpty else { return }; task?.cancel(); generation += 1; run(after: nil) }
}
