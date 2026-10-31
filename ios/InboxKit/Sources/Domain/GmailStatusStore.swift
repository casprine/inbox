import Foundation
import Observation
import API

/// App-wide Gmail connection state: Mail shows the signed-out banner from it, Settings→Gmail renders it, SSE keeps it current.
@Observable
public final class GmailStatusStore: @unchecked Sendable {
    public private(set) var status: GmailStatus?
    /// Highest "waiting" count seen since the queue was last empty; "9 of 14" progress = (peak - waiting) / peak.
    public private(set) var peakWaiting = 0
    public private(set) var failedToLoad = false

    public init(status: GmailStatus? = nil) { self.status = status; peakWaiting = status?.waiting ?? 0 }

    public var needsReconnect: Bool { status?.state == .needsReauth }
    public var isConnected: Bool { status?.state == .connected }
    public var isCatchingUp: Bool { isConnected && (status?.waiting ?? 0) > 0 }
    public var done: Int { max(peakWaiting - (status?.waiting ?? 0), 0) }

    @MainActor public func refresh(_ api: APIClient) async {
        do { set(try await api.gmailStatus()); failedToLoad = false } catch { failedToLoad = true }
    }

    @MainActor public func set(_ s: GmailStatus) {
        status = s
        if s.waiting == 0 { peakWaiting = 0 } else { peakWaiting = max(peakWaiting, s.waiting) }
    }

    /// Merges an SSE `gmail.status` event (state, email, waiting, rate_limited_until).
    @MainActor public func apply(_ fields: [String: JSONValue]) {
        var s = status ?? GmailStatus(state: .notConnected)
        if let raw = fields["state"]?.stringValue, let st = GmailStatus.State(rawValue: raw) { s.state = st }
        if case .string(let e)? = fields["email"] { s.email = e }
        if case .number(let n)? = fields["waiting"] { s.waiting = Int(n) }
        if let raw = fields["rate_limited_until"] {
            if case .string(let d) = raw { s.rateLimitedUntil = APIClient.parseDate(d) } else if case .null = raw { s.rateLimitedUntil = nil }
        }
        set(s)
    }
}

/// Result of the OAuth round trip, read from `inbox://gmail/connected?status=ok|error[&reason=…]`.
public enum GmailConnectResult: Equatable, Sendable {
    case connected
    case failed(reason: String)

    public static func parse(_ url: URL) -> GmailConnectResult? {
        guard url.scheme == "inbox", url.host == "gmail", url.path == "/connected" || url.path.isEmpty || url.lastPathComponent == "connected" else { return nil }
        let items = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
        let status = items.first { $0.name == "status" }?.value
        if status == "ok" { return .connected }
        return .failed(reason: items.first { $0.name == "reason" }?.value ?? "internal")
    }

    /// Plain-words explanation for a failure reason; `nil` for the user simply cancelling.
    public static func message(for reason: String) -> String? {
        switch reason {
        case "access_denied": "You didn't allow access, so Gmail stays disconnected."
        case "no_refresh_token": "Google didn't give Inbox long-term access. Remove Inbox from your Google account's third-party access, then try again."
        case "not_configured": "Gmail isn't set up on your server yet."
        case "invalid_state": "That sign-in link expired. Start again."
        default: "Couldn't connect Gmail. Try again."
        }
    }
}
