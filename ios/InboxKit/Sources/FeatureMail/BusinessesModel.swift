import Foundation
import Observation
import API
import Domain
import Persistence

/// Mail root: businesses with unread counts and the Needs-attention group.
@MainActor @Observable
public final class BusinessesModel {
    public private(set) var businesses: [BusinessRecord] = []
    public private(set) var unroutedCount = 0
    public private(set) var failedCount = 0
    public private(set) var hasLoaded = false
    public private(set) var refreshFailed = false
    private let env: AppEnvironment

    public init(env: AppEnvironment) { self.env = env }

    /// Hidden or disabled businesses don't appear in Mail.
    public var visible: [BusinessRecord] { businesses.filter { !$0.hidden && $0.enabled } }
    public var totalUnread: Int { visible.reduce(0) { $0 + $1.unread } }
    public var attentionTotal: Int { unroutedCount + failedCount }
    public var showsAttention: Bool { attentionTotal > 0 }
    /// Skeleton while there is neither a cache nor a completed refresh.
    public var isLoading: Bool { !hasLoaded && businesses.isEmpty }

    /// Follows the cache until cancelled.
    public func observe() async {
        await loadCache()
        for await list in env.businesses.observe() { businesses = list; if !list.isEmpty { hasLoaded = true } }
    }

    public func loadCache() async {
        if let list = try? await env.database.businesses() { businesses = list }
        let u = try? await env.database.pref("attention.unrouted")
        let f = try? await env.database.pref("attention.failed")
        unroutedCount = Int(u ?? "") ?? 0
        failedCount = Int(f ?? "") ?? 0
    }

    public func refresh() async {
        await loadCache()
        do {
            try await env.businesses.refresh()
            refreshFailed = false
        } catch { refreshFailed = true }
        if let a = try? await env.api.attention() {
            unroutedCount = a.unrouted.count; failedCount = a.failed.count
            try? await env.database.setPref("attention.unrouted", String(a.unrouted.count))
            try? await env.database.setPref("attention.failed", String(a.failed.count))
        }
        hasLoaded = true
    }
}
