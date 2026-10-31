import Foundation
import Observation
import API
import Domain
import Persistence

public struct ToastState: Equatable, Sendable, Identifiable {
    public enum Kind: Sendable, Equatable { case archived, notice }
    public let id = UUID()
    public var message: String
    public var kind: Kind
    public var ids: [UUID]
}

public struct AddressChip: Identifiable, Hashable, Sendable {
    /// nil is the "All" chip.
    public var address: String?
    public var count: Int
    public var id: String { address ?? "" }
    public var title: String { address.map { "\($0)@" } ?? "All" }
}

public struct MessageSection: Identifiable, Equatable {
    public var id: String
    public var title: String?
    public var messages: [MessageRecord]
}

/// Business page (scope `.business`) and All businesses (scope `.all`).
@MainActor @Observable
public final class MessageListModel {
    public enum Scope: Hashable, Sendable { case business(UUID), all }

    public let scope: Scope
    public private(set) var businesses: [BusinessRecord] = []
    public private(set) var messages: [MessageRecord] = []
    public var selectedAddress: String?
    public var unreadOnly = false
    public private(set) var hasLoaded = false
    public private(set) var refreshFailed = false
    public private(set) var nextCursor: String?
    public private(set) var isLoadingMore = false

    public private(set) var isSelecting = false
    public private(set) var selection: Set<UUID> = []
    public private(set) var toast: ToastState?

    private let env: AppEnvironment
    private let toastDuration: Duration
    private var toastTask: Task<Void, Never>?

    public init(env: AppEnvironment, scope: Scope, selectedAddress: String? = nil, toastDuration: Duration = .seconds(5)) {
        self.env = env; self.scope = scope; self.selectedAddress = selectedAddress; self.toastDuration = toastDuration
    }

    // MARK: Derived

    private var filter: MessageFilter {
        if case .business(let id) = scope { return MessageFilter(businessId: id) }
        return MessageFilter()
    }

    public var business: BusinessRecord? {
        if case .business(let id) = scope { return businesses.first { $0.id == id } }
        return nil
    }

    public var chips: [AddressChip] {
        guard let b = business else { return [] }
        let addrs = b.addresses
        return [AddressChip(address: nil, count: addrs.reduce(0) { $0 + $1.count })]
            + addrs.map { AddressChip(address: $0.localPart, count: $0.count) }
    }

    public var totalUnread: Int {
        switch scope {
        case .business: business?.unread ?? 0
        case .all: businesses.filter { !$0.hidden && $0.enabled }.reduce(0) { $0 + $1.unread }
        }
    }

    public var visibleMessages: [MessageRecord] {
        messages.filter { m in
            if unreadOnly && m.isRead { return false }
            if let a = selectedAddress, m.toLocal?.lowercased() != a.lowercased() { return false }
            return true
        }
    }

    /// Newest-first flat list for a business; grouped by business (user order, unassigned last) for All.
    public var sections: [MessageSection] {
        let rows = visibleMessages
        guard case .all = scope else { return rows.isEmpty ? [] : [MessageSection(id: "flat", title: nil, messages: rows)] }
        var out: [MessageSection] = []
        for b in businesses where !b.hidden && b.enabled {
            let g = rows.filter { $0.businessId == b.id }
            if !g.isEmpty { out.append(MessageSection(id: b.id.uuidString, title: b.name, messages: g)) }
        }
        let known = Set(businesses.map(\.id))
        let rest = rows.filter { $0.businessId == nil || !known.contains($0.businessId!) }
        if !rest.isEmpty { out.append(MessageSection(id: "none", title: "Unassigned", messages: rest)) }
        return out
    }

    public var isLoading: Bool { !hasLoaded && messages.isEmpty }
    public var isEmpty: Bool { hasLoaded && visibleMessages.isEmpty }
    /// Distinguishes the two empty states: nothing at all vs. nothing unread.
    public var emptyKind: EmptyKind { unreadOnly && !messages.isEmpty ? .allRead : .noMail }
    public enum EmptyKind: Sendable { case noMail, allRead }

    // MARK: Loading

    public func observe() async {
        let biz = env.businesses.observe(), msgs = env.mail.observe(filter)
        let t1 = Task { for await b in biz { self.businesses = b } }
        let t2 = Task { for await m in msgs { self.messages = m; if !m.isEmpty { self.hasLoaded = true } } }
        await withTaskCancellationHandler {
            await t1.value; await t2.value
        } onCancel: { t1.cancel(); t2.cancel() }
    }

    /// One-shot read of the cache (what `observe` streams); also used by tests.
    public func loadCache() async {
        businesses = (try? await env.database.businesses()) ?? businesses
        messages = (try? await env.database.messages(filter)) ?? messages
    }

    public func refresh() async {
        do {
            nextCursor = try await env.mail.refresh(filter)
            try? await env.businesses.refresh()
            refreshFailed = false
        } catch { refreshFailed = true }
        hasLoaded = true
    }

    public func loadMore() async {
        guard let cursor = nextCursor, !isLoadingMore else { return }
        isLoadingMore = true
        defer { isLoadingMore = false }
        do { nextCursor = try await env.mail.refresh(filter, cursor: cursor) } catch { refreshFailed = true }
    }

    // MARK: Selection

    public func beginSelecting(_ id: UUID? = nil) {
        isSelecting = true
        if let id { selection.insert(id) }
    }
    public func toggleSelection(_ id: UUID) {
        if !selection.insert(id).inserted { selection.remove(id) }
    }
    public func selectAll() { selection = Set(visibleMessages.map(\.id)) }
    public func endSelecting() { isSelecting = false; selection = [] }

    // MARK: Actions

    /// Marks read when any target is unread, otherwise unread (the bulk "Mark read" and the row swipe).
    public func toggleRead(_ ids: Set<UUID>) async {
        let targets = messages.filter { ids.contains($0.id) }
        guard !targets.isEmpty else { return }
        let makeRead = targets.contains { !$0.isRead }
        try? await env.mail.setRead(makeRead, ids: targets.map(\.id))
        if isSelecting { endSelecting() }
    }

    public func archive(_ ids: Set<UUID>) async {
        let list = messages.filter { ids.contains($0.id) }.map(\.id)
        guard !list.isEmpty else { return }
        try? await env.mail.setArchived(true, ids: list)
        endSelecting()
        showToast(ToastState(message: list.count == 1 ? "Archived" : "\(list.count) archived", kind: .archived, ids: list))
    }

    /// Cancels the queued archive (outbox inverse) and restores the rows.
    public func undo() async {
        guard let t = toast, t.kind == .archived else { return }
        dismissToast()
        try? await env.mail.setArchived(false, ids: t.ids)
    }

    public func openInGmail() {
        // Deep link to Gmail arrives with the Gmail feature.
        endSelecting()
        showToast(ToastState(message: "Open in Gmail isn't available yet", kind: .notice, ids: []))
    }

    public func dismissToast() { toastTask?.cancel(); toastTask = nil; toast = nil }

    private func showToast(_ t: ToastState) {
        toastTask?.cancel()
        toast = t
        let id = t.id, d = toastDuration
        toastTask = Task { [weak self] in
            try? await Task.sleep(for: d)
            guard !Task.isCancelled, let self, self.toast?.id == id else { return }
            self.toast = nil
        }
    }
}
