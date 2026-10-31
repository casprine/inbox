import Foundation
import Observation
import API

/// App-wide import state: Mail's first-open banner, Settings → Import older mail and Search's "still importing" hint read from it;
/// SSE `job.progress` keeps it current.
@Observable
public final class BackfillStore: @unchecked Sendable {
    public private(set) var backfills: [Backfill] = []
    public private(set) var failedToLoad = false
    public private(set) var hasLoaded = false
    /// Range (in days) the user last started; used for "Importing the last 90 days".
    public private(set) var days: Int?
    public init() {}

    /// Days covered, from the oldest `since` of the latest batch (works after a relaunch, when `days` is unknown).
    public var rangeDays: Int? {
        let f = DateFormatter(); f.dateFormat = "yyyy-MM-dd"; f.locale = Locale(identifier: "en_US_POSIX"); f.timeZone = .gmt
        guard let s = latestBatch.compactMap({ f.date(from: $0.since) }).min() else { return nil }
        let d = Calendar.current.dateComponents([.day], from: s, to: latestBatch.compactMap(\.createdAt).max() ?? .now).day ?? 0
        return [30, 90, 365].min { abs($0 - d) < abs($1 - d) } ?? d
    }

    public var active: [Backfill] { backfills.filter(\.isActive) }
    public var isActive: Bool { !active.isEmpty }
    public var isRunning: Bool { backfills.contains { $0.status == .running || $0.status == .queued } }
    public var isPaused: Bool { isActive && active.allSatisfy { $0.status == .paused } }
    /// Newest backfill batch (the ones created together): everything created within a minute of the newest.
    public var latestBatch: [Backfill] {
        guard let newest = backfills.compactMap(\.createdAt).max() else { return backfills }
        return backfills.filter { ($0.createdAt ?? .distantPast) > newest.addingTimeInterval(-60) }
    }
    public var batchEstimated: Int { latestBatch.reduce(0) { $0 + $1.estimated } }
    public var batchScanned: Int { latestBatch.reduce(0) { $0 + $1.scanned } }
    public var batchImported: Int { latestBatch.reduce(0) { $0 + $1.imported } }
    public var batchSorted: Int { latestBatch.reduce(0) { $0 + $1.sorted } }
    public var batchUnrouted: Int { latestBatch.reduce(0) { $0 + $1.unrouted } }
    public var batchFinished: Bool { !latestBatch.isEmpty && latestBatch.allSatisfy { !$0.isActive } }
    public var progress: Double { batchEstimated == 0 ? 0 : min(Double(batchScanned) / Double(batchEstimated), 1) }
    public var pausedUntil: Date? { active.compactMap(\.pausedUntil).min() }
    public var pauseReason: String? { active.first { $0.status == .paused }?.pauseReason }

    @MainActor public func refresh(_ api: APIClient) async {
        do { set(try await api.backfills()); failedToLoad = false } catch { failedToLoad = true }
        hasLoaded = true
    }
    @MainActor public func set(_ list: [Backfill]) { backfills = list }
    @MainActor public func noteStarted(days: Int?) { self.days = days }

    /// Merges an SSE `job.progress` event for a backfill (counts, status, paused_until).
    @MainActor public func apply(_ f: [String: JSONValue]) {
        guard f["job"]?.stringValue == "backfill", let raw = f["backfill_id"]?.stringValue, let id = UUID(uuidString: raw),
              let i = backfills.firstIndex(where: { $0.id == id }) else { return }
        var b = backfills[i]
        if let s = f["status"]?.stringValue, let st = Backfill.Status(rawValue: s) { b.status = st }
        func n(_ k: String) -> Int? { if case .number(let v)? = f[k] { Int(v) } else { nil } }
        b.estimated = n("estimated") ?? b.estimated; b.scanned = n("scanned") ?? b.scanned; b.imported = n("imported") ?? b.imported
        b.sorted = n("sorted") ?? b.sorted; b.unrouted = n("unrouted") ?? b.unrouted; b.skipped = n("skipped") ?? b.skipped
        if let raw = f["paused_until"] { if case .string(let d) = raw { b.pausedUntil = APIClient.parseDate(d) } else if case .null = raw { b.pausedUntil = nil } }
        backfills[i] = b
    }
}
