import Foundation
import Network
import UIKit
import API

/// Message-count estimates per import range (OB09). The real `/v1/backfills:estimate` arrives with T25/T26; until then
/// the numbers are the board's examples and the screen says "About".
public protocol BackfillEstimating: Sendable {
    func estimates() async -> [Int: Int]
}
/// `GET /v1/backfills:estimate` per range; falls back to the board's examples when Gmail isn't connected or the call fails.
public struct APIBackfillEstimator: BackfillEstimating {
    let api: APIClient
    public init(api: APIClient) { self.api = api }
    public func estimates() async -> [Int: Int] {
        var out: [Int: Int] = [:]
        for d in [30, 90, 365] { if let e = try? await api.backfillEstimate(days: d) { out[d] = e.total } }
        return out.isEmpty ? await StubBackfillEstimator().estimates() : out
    }
}
public struct StubBackfillEstimator: BackfillEstimating {
    public init() {}
    public func estimates() async -> [Int: Int] { [30: 1_100, 90: 3_200, 365: 12_800] }
}

/// An Alfather found on the LAN (Bonjour `_alfather._tcp`).
public struct AlfatherService: Sendable, Hashable {
    public var name: String
    /// Shown to the person, e.g. "alfather.local".
    public var host: String
    public var endpoint: NWEndpoint?
    public init(name: String, host: String, endpoint: NWEndpoint? = nil) { self.name = name; self.host = host; self.endpoint = endpoint }
}

/// Finds Alfather on the local network (OB10/OB11) and hands it the pairing credentials.
public protocol AlfatherDiscovering: Sendable {
    /// First Alfather found, or nil after the search window.
    func discover(timeout: Duration) async -> AlfatherService?
    /// Delivers `mcp_url` + token to Alfather over the LAN. Throws when it doesn't accept them.
    func deliver(_ grant: AlfatherPairingGrant, to service: AlfatherService) async throws
}

public struct BonjourAlfatherDiscovery: AlfatherDiscovering {
    public init() {}
    public func discover(timeout: Duration = .seconds(4)) async -> AlfatherService? {
        await withTaskGroup(of: AlfatherService?.self) { group in
            group.addTask {
                let found = AsyncStream<AlfatherService> { cont in
                    let browser = NWBrowser(for: .bonjour(type: "_alfather._tcp", domain: nil), using: .tcp)
                    browser.browseResultsChangedHandler = { results, _ in
                        for r in results { if case let .service(name, _, _, _) = r.endpoint { cont.yield(AlfatherService(name: name, host: "\(name).local", endpoint: r.endpoint)); return } }
                    }
                    browser.start(queue: .global())
                    cont.onTermination = { _ in browser.cancel() }
                }
                for await s in found { return s }
                return nil
            }
            group.addTask { try? await Task.sleep(for: timeout); return nil }
            let first = await group.next() ?? nil
            group.cancelAll()
            return first
        }
    }

    /// Assumed Alfather contract (Q-T31-3): `POST /pair` with `{mcp_url, token, scope}` answers 2xx once it has stored them.
    /// Sent as a small HTTP/1.1 request straight over the Bonjour connection.
    public func deliver(_ grant: AlfatherPairingGrant, to service: AlfatherService) async throws {
        guard let endpoint = service.endpoint else { throw URLError(.cannotFindHost) }
        let body = try JSONSerialization.data(withJSONObject: ["mcp_url": grant.mcpUrl, "token": grant.token, "scope": grant.scope])
        let req: Data = {
            var d = Data("POST /pair HTTP/1.1\r\nHost: \(service.host)\r\nContent-Type: application/json\r\nContent-Length: \(body.count)\r\nConnection: close\r\n\r\n".utf8)
            d.append(body); return d
        }()
        let conn = NWConnection(to: endpoint, using: .tcp)
        let status: Int = try await withCheckedThrowingContinuation { cont in
            let done = Mutex<Bool>(false)
            let finish: @Sendable (Result<Int, Error>) -> Void = { r in
                if done.withLock({ let was = $0; $0 = true; return was }) { return }
                conn.cancel(); cont.resume(with: r)
            }
            conn.stateUpdateHandler = { state in
                switch state {
                case .ready:
                    conn.send(content: req, completion: .contentProcessed { err in if let err { finish(.failure(err)) } })
                    conn.receive(minimumIncompleteLength: 12, maximumLength: 4096) { data, _, _, err in
                        if let err { finish(.failure(err)); return }
                        let line = data.flatMap { String(data: $0, encoding: .utf8) }?.split(separator: "\r\n").first.map(String.init) ?? ""
                        finish(.success(Int(line.split(separator: " ").dropFirst().first ?? "") ?? 0))
                    }
                case .failed(let e): finish(.failure(e))
                case .waiting(let e): finish(.failure(e))
                default: break
                }
            }
            conn.start(queue: .global())
            DispatchQueue.global().asyncAfter(deadline: .now() + 8) { finish(.failure(URLError(.timedOut))) }
        }
        guard (200..<300).contains(status) else { throw URLError(.badServerResponse) }
    }
}

/// Mints the server-side pairing (T31) and hands it to the Alfather found on the LAN. Used by onboarding and Settings → Alfather.
public struct AlfatherPairer: Sendable {
    let api: APIClient
    let discovery: any AlfatherDiscovering
    public init(api: APIClient, discovery: any AlfatherDiscovering) { self.api = api; self.discovery = discovery }
    public func pair(_ service: AlfatherService) async throws {
        let grant = try await api.createAlfatherPairing()
        try await discovery.deliver(grant, to: service)
    }
}

// MARK: Mock-mode fakes (`-UseMockData -Onboarding 1`)

public struct MockAlfatherDiscovery: AlfatherDiscovering {
    public let host: String?
    public init(host: String? = "alfather.local") { self.host = host }
    public func discover(timeout: Duration) async -> AlfatherService? { host.map { AlfatherService(name: "alfather", host: $0) } }
    public func deliver(_ grant: AlfatherPairingGrant, to service: AlfatherService) async throws {}
}

private final class Mutex<Value>: @unchecked Sendable {
    private let lock = NSLock(); private var value: Value
    init(_ v: Value) { value = v }
    func withLock<R>(_ body: (inout Value) -> R) -> R { lock.lock(); defer { lock.unlock() }; return body(&value) }
}
