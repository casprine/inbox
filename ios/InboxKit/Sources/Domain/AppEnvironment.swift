import Foundation
import SwiftUI
import API
import Persistence

/// Composition root. `live()` for the app, `preview()` for SwiftUI previews and tests (in-memory DB, offline API).
public struct AppEnvironment: Sendable {
    public let api: APIClient
    public let sse: SSEClient
    public let tokenStore: any TokenStore
    public let database: AppDatabase
    public let businesses: BusinessRepository
    public let mail: MailRepository
    public let outbox: OutboxWorker
    public let rules: RulesRepository
    /// Gmail labels; a stub until the Gmail feature (T20) provides the real client.
    public let labels: any LabelsProviding
    public let gmail: GmailStatusStore
    public let theme: ThemePreference
    public let backfill: BackfillStore
    public let session: SessionMonitor

    public init(api: APIClient, tokenStore: any TokenStore, database: AppDatabase, reachability: any Reachability, labels: (any LabelsProviding)? = nil, gmail: GmailStatusStore = GmailStatusStore()) {
        self.labels = labels ?? APILabelsProvider(api: api)
        self.gmail = gmail
        theme = ThemePreference()
        backfill = BackfillStore()
        session = SessionMonitor()
        rules = RulesRepository(api: api, db: database)
        self.api = api; self.tokenStore = tokenStore; self.database = database
        sse = SSEClient(api: api)
        session.attach(to: api)
        outbox = OutboxWorker(api: api, db: database, reachability: reachability)
        businesses = BusinessRepository(api: api, db: database)
        mail = MailRepository(api: api, db: database, outbox: outbox)
    }

    /// Base URL: `INBOX_API_BASE_URL` env (dev), else UserDefaults `api_base_url`, else the `InboxAPIBaseURL` Info.plist value baked in
    /// at archive time, else http://localhost:8080.
    public static func defaultBaseURL() -> URL {
        let env = DevConfig.baseURL
        let plist = (Bundle.main.object(forInfoDictionaryKey: "InboxAPIBaseURL") as? String).flatMap { $0.isEmpty ? nil : $0 }
        let s = env ?? UserDefaults.standard.string(forKey: "api_base_url") ?? plist
        return s.flatMap(URL.init(string:)) ?? URL(string: "http://localhost:8080")!
    }

    public static func live(baseURL: URL = defaultBaseURL()) throws -> AppEnvironment {
        let dir = try FileManager.default.url(for: .applicationSupportDirectory, in: .userDomainMask, appropriateFor: nil, create: true)
        let db = try AppDatabase.open(at: dir.appendingPathComponent("inbox.sqlite"))
        // Dev server (docs/DEV_SERVER.md): a fixed token (env or Local.xcconfig, see DevConfig) stands in for the password sign-in.
        // Held in memory because unsigned simulator builds can't always reach the Keychain. Nil in store builds.
        let store: any TokenStore = DevConfig.token.map { InMemoryTokenStore($0) } ?? KeychainTokenStore()
        return AppEnvironment(api: APIClient(baseURL: baseURL, tokenStore: store), tokenStore: store, database: db,
                              reachability: PathMonitorReachability())
    }

    /// In-memory database and an API that always answers 503 (no network); seed the DB for previews.
    public static func preview() throws -> AppEnvironment {
        let cfg = URLSessionConfiguration.ephemeral
        cfg.protocolClasses = [OfflineURLProtocol.self]
        let store = InMemoryTokenStore("preview")
        let api = APIClient(baseURL: URL(string: "http://preview.invalid")!, tokenStore: store, session: URLSession(configuration: cfg))
        return AppEnvironment(api: api, tokenStore: store, database: try .inMemory(), reachability: OfflineReachability())
    }
}

struct OfflineReachability: Reachability {
    func updates() -> AsyncStream<Bool> { AsyncStream { $0.yield(false) } }
}

final class OfflineURLProtocol: URLProtocol, @unchecked Sendable {
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        if let url = request.url, let r = MockRules.response(for: request) ?? MockContent.response(for: url),
           let resp = HTTPURLResponse(url: url, statusCode: r.status, httpVersion: "HTTP/1.1", headerFields: r.headers) {
            client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: r.body)
            client?.urlProtocolDidFinishLoading(self)
            return
        }
        client?.urlProtocol(self, didFailWithError: URLError(.notConnectedToInternet))
    }
    override func stopLoading() {}
}

private struct AppEnvironmentKey: EnvironmentKey { static let defaultValue: AppEnvironment? = nil }
extension EnvironmentValues {
    public var appEnvironment: AppEnvironment? {
        get { self[AppEnvironmentKey.self] }
        set { self[AppEnvironmentKey.self] = newValue }
    }
}
