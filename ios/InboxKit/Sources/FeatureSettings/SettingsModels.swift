import Foundation
import Observation
import API
import Domain
import FeatureOnboarding

/// Health (ST05/ST06): what is working and what needs you, from `GET /v1/health`.
@MainActor @Observable
public final class HealthModel {
    public private(set) var report: HealthReport?
    public private(set) var isLoading = true
    public private(set) var failed = false
    private let env: AppEnvironment
    public init(env: AppEnvironment, report: HealthReport? = nil) { self.env = env; self.report = report; if report != nil { isLoading = false } }

    public func load() async {
        do { report = try await env.api.health(); failed = false } catch { failed = true }
        isLoading = false
    }
    public var headline: String {
        guard let r = report else { return "" }
        let n = r.problems.count
        return n == 0 ? "All good" : "\(n) thing\(n == 1 ? "" : "s") need\(n == 1 ? "s" : "") you"
    }
    /// Value shown on the Settings row.
    public var rowValue: String { report == nil ? "" : (report!.problems.isEmpty ? "All good" : "Needs you") }
    /// Checks worth showing: `off` (not set up) rows stay visible but quiet.
    public var checks: [HealthCheck] { report?.checks ?? [] }

    /// Rows as the board shows them: one "Mail arriving" row (the worst business wins) followed by Worker, Gmail, Queue, Alfather.
    public var rows: [HealthCheck] {
        let all = checks
        var out: [HealthCheck] = []
        let mail = all.filter { $0.id == "mail_arriving" }
        if !mail.isEmpty {
            let rank: (HealthCheck.State) -> Int = { s in switch s { case .error: 3; case .warn: 2; case .ok: 1; case .off: 0 } }
            let worst = mail.max { rank($0.state) < rank($1.state) }!
            let bad = worst.state == .warn || worst.state == .error
            out.append(HealthCheck(id: "mail_arriving", businessId: bad ? worst.businessId : nil, title: "Mail arriving", state: bad ? worst.state : .ok,
                                   message: bad ? worst.message : (mail.count == 1 ? "Mail is arriving" : "Mail is arriving for all \(mail.count) businesses"),
                                   fixRoute: bad ? worst.fixRoute : nil))
        }
        let order = ["worker", "gmail", "queue", "alfather"]
        for id in order { out.append(contentsOf: all.filter { $0.id == id }) }
        out.append(contentsOf: all.filter { $0.id != "mail_arriving" && !order.contains($0.id) })
        return out
    }

    /// "Checked 1 min ago".
    public func checkedText(now: Date = .now) -> String {
        guard let d = report?.checkedAt else { return "" }
        let s = max(Int(now.timeIntervalSince(d)), 0)
        if s < 60 { return "Checked just now" }
        let m = s / 60
        return m < 60 ? "Checked \(m) min ago" : "Checked \(m / 60) h ago"
    }

    /// Maps a server `fix_route` to a Settings route; nil when there is nothing to open.
    public nonisolated static func route(for fix: String?) -> SettingsRoute? {
        guard let fix else { return nil }
        if fix == "gmail" { return .gmail }
        if fix == "alfather" { return .alfather }
        let parts = fix.split(separator: "/").map(String.init)
        if parts.count == 3, parts[0] == "business", parts[2] == "routing", let id = UUID(uuidString: parts[1]) { return .routing(id) }
        return nil
    }
}

/// Alfather status (AF01–AF04) from `GET /v1/alfather/status`; pairing mints a token on the server and hands it to Alfather over the LAN.
@MainActor @Observable
public final class AlfatherModel {
    public private(set) var status = AlfatherStatus(paired: false, connected: false)
    public private(set) var isLoading = true
    public private(set) var pairing = false
    public private(set) var error: String?
    private let env: AppEnvironment
    private let discovery: any AlfatherDiscovering
    public init(env: AppEnvironment, discovery: any AlfatherDiscovering) { self.env = env; self.discovery = discovery }

    public var state: AlfatherStatus.State { status.state() }

    public func load() async {
        do { status = try await env.api.alfatherStatus() ?? AlfatherStatus(paired: false, connected: false); error = nil }
        catch { self.error = "Couldn't reach your Inbox server." }
        isLoading = false
    }
    /// Look on the LAN, then pair.
    public func pairNow() async {
        pairing = true; error = nil; defer { pairing = false }
        guard let service = await discovery.discover(timeout: .seconds(4)) else { error = "Alfather wasn't found on this network. Check it's running on the same Wi-Fi."; return }
        do { try await AlfatherPairer(api: env.api, discovery: discovery).pair(service); await load() }
        catch { self.error = "Alfather didn't accept the pairing. Check it's running and try again." }
    }
    public func unpair() async {
        do { try await env.api.unpairAlfather(); await load() } catch { self.error = "Couldn't unpair. Try again." }
    }
    public var lastSeenText: String {
        guard let d = status.lastSeen else { return "" }
        if Date.now.timeIntervalSince(d) < 90 { return "last seen just now" }
        return "last seen \(RelativeDay.string(d))"
    }
}
