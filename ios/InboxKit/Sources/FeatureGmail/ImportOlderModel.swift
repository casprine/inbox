import Foundation
import Observation
import API
import Domain
import Persistence

/// Settings → Import older mail (GM06 setup, GM07 running, GM08 done, GM09 paused).
@MainActor @Observable
public final class ImportOlderModel {
    public enum Range: Hashable, Sendable { case days(Int), custom }
    public var range: Range = .days(90) { didSet { if range != oldValue { Task { await refreshEstimate() } } } }
    public var customSince = Calendar.current.date(byAdding: .month, value: -6, to: AppClock.now) ?? AppClock.now
    public var chosenBusinesses: Set<UUID> = []             // empty = all enabled businesses
    public private(set) var businesses: [BusinessRecord] = []
    public private(set) var estimate: BackfillEstimate?
    public private(set) var estimating = false
    public private(set) var starting = false
    public private(set) var message: String?
    public var confirmingCancel = false
    private let env: AppEnvironment
    public init(env: AppEnvironment) { self.env = env }

    public var store: BackfillStore { env.backfill }

    public func load() async {
        businesses = ((try? await env.businesses.all()) ?? []).filter(\.enabled)
        await env.backfill.refresh(env.api)
        await refreshEstimate()
    }

    private var days: Int? { if case .days(let d) = range { d } else { nil } }
    private var sinceString: String? {
        guard case .custom = range else { return nil }
        let f = DateFormatter(); f.dateFormat = "yyyy-MM-dd"; f.locale = Locale(identifier: "en_US_POSIX"); f.timeZone = .gmt
        return f.string(from: customSince)
    }
    private var businessIDs: [UUID]? { chosenBusinesses.isEmpty ? nil : Array(chosenBusinesses) }

    public func refreshEstimate() async {
        estimating = true; defer { estimating = false }
        do { estimate = try await env.api.backfillEstimate(days: days, since: sinceString, businesses: businessIDs); message = nil }
        catch let e as APIError { estimate = nil; message = Self.explain(e) }
        catch { estimate = nil }
    }

    public var rangeTitle: String {
        switch range { case .days(30): "last 30 days"; case .days(90): "last 90 days"; case .days(365): "last year"; case .days(let d): "last \(d) days"
        case .custom: "since \(customSince.formatted(date: .abbreviated, time: .omitted))" }
    }
    public var businessSummary: String {
        chosenBusinesses.isEmpty ? "All \(businesses.count) business\(businesses.count == 1 ? "" : "es")"
        : "\(chosenBusinesses.count) of \(businesses.count) businesses"
    }
    public var etaText: String? {
        guard let s = estimate?.etaSeconds, s > 0 else { return nil }
        return s < 90 ? "about a minute" : "about \(Int((Double(s) / 60).rounded())) min"
    }

    public func start() async {
        guard !starting else { return }
        starting = true; defer { starting = false }
        do {
            _ = try await env.api.startBackfill(days: days, since: sinceString, businesses: businessIDs)
            env.backfill.noteStarted(days: days)
            await env.backfill.refresh(env.api); message = nil
        } catch let e as APIError { message = Self.explain(e) } catch { message = "Couldn't start the import. Try again." }
    }

    public func pause() async { await act { try await self.env.api.pauseBackfill(id: $0) } }
    public func resume() async { await act { try await self.env.api.resumeBackfill(id: $0) } }
    public func cancel() async { await act { try await self.env.api.cancelBackfill(id: $0) } }

    private func act(_ call: @escaping (UUID) async throws -> Backfill) async {
        for b in env.backfill.active { do { _ = try await call(b.id) } catch { message = "Couldn't change the import. Try again." } }
        await env.backfill.refresh(env.api)
    }

    public static func explain(_ e: APIError) -> String {
        if case .problem(let p, _) = e {
            switch p.code {
            case "gmail_not_connected": return "Connect Gmail first, in Settings → Gmail."
            case "gmail_needs_reauth": return "Gmail signed Inbox out. Reconnect in Settings → Gmail."
            case "backfill_active": return "One of these businesses is already importing."
            case "invalid_range": return "That date range isn't valid."
            default: break
            }
        }
        return e.isRetryable ? "Couldn't reach the server. Try again." : "Couldn't do that. Try again."
    }

    /// Plain words for a pause: the reason and when it ends.
    public func pauseText(now: Date = AppClock.now) -> (title: String, detail: String) {
        switch store.pauseReason {
        case "rate_limited":
            let when = store.pausedUntil.map { " around \($0.formatted(date: .omitted, time: .shortened))" } ?? " soon"
            return ("Paused by Gmail rate limits.", "The import resumes on its own\(when). Nothing is lost.")
        case "gmail_signed_out": return ("Paused: Gmail signed out", "Reconnect Gmail and the import carries on where it stopped.")
        default: return ("Import paused", "Resume it whenever you like.")
        }
    }
}
