import Foundation
import Observation
import API

/// Bridges the single SSE connection to the UI: `tick` changes on every `mail.new` (and on reconnect), and
/// views refresh with `.task(id: live.tick)`.
@MainActor @Observable
public final class LiveEvents {
    public private(set) var tick = 0
    /// False in mock mode (no server to refresh from).
    public var refreshEnabled = true
    public init() {}

    public func handle(_ update: SSEClient.Update) {
        switch update {
        case .event(.mailNew), .reconnected: tick += 1
        case .event, .unauthorized: break
        }
    }

    /// Runs until cancelled.
    public func run(_ sse: SSEClient, onGmail: @MainActor (_ fields: [String: JSONValue]) -> Void = { _ in },
                    onJob: @MainActor (_ fields: [String: JSONValue]) -> Void = { _ in }) async {
        for await u in sse.events() {
            handle(u)
            if case .event(.gmailStatus(let f)) = u { onGmail(f) }
            if case .event(.jobProgress(let f)) = u { onJob(f) }
        }
    }
}
