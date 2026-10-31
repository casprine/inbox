import Foundation
import os

public struct APIConfig: Sendable {
    public var baseURL: URL
    public init(baseURL: URL) { self.baseURL = baseURL }
}

/// URLSession client for the Inbox server (api/openapi.yaml). Stateless apart from the token store.
public final class APIClient: Sendable {
    private let base: OSAllocatedUnfairLock<URL>
    /// The server this client talks to. Changes once, when a sign-in succeeds against a different address.
    public var baseURL: URL { base.withLock { $0 } }
    private let tokens: any TokenStore
    private let session: URLSession
    private let makeKey: @Sendable () -> String
    private let unauthorizedHandler = OSAllocatedUnfairLock<(@Sendable () -> Void)?>(initialState: nil)

    public init(baseURL: URL, tokenStore: any TokenStore, session: URLSession = .shared,
                idempotencyKey: @escaping @Sendable () -> String = { UUID().uuidString }) {
        self.base = OSAllocatedUnfairLock(initialState: baseURL)
        self.tokens = tokenStore
        self.session = session
        self.makeKey = idempotencyKey
    }

    /// Called once each time a token that was valid stops being accepted (401 on an authenticated call). The token is already cleared then.
    public func onUnauthorized(_ handler: (@Sendable () -> Void)?) { unauthorizedHandler.withLock { $0 = handler } }

    /// A 401 for a request that carried `sentToken`: forget that token (never a newer one from a sign-in that finished meanwhile) and tell
    /// the app once. Later calls then fail with `notAuthenticated` without touching the network, so nothing retries against the server.
    func tokenRejected(_ sentToken: String) async {
        guard await tokens.token() == sentToken else { return }
        await tokens.clear()
        let h = unauthorizedHandler.withLock { $0 }
        h?()
    }

    // MARK: JSON coding

    public static func makeDecoder() -> JSONDecoder {
        let d = JSONDecoder()
        d.keyDecodingStrategy = .convertFromSnakeCase
        d.dateDecodingStrategy = .custom { decoder in
            let s = try decoder.singleValueContainer().decode(String.self)
            if let date = Self.parseDate(s) { return date }
            throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: "bad date \(s)"))
        }
        return d
    }

    public static func parseDate(_ s: String) -> Date? {
        if let d = try? Date.ISO8601FormatStyle(includingFractionalSeconds: true).parse(s) { return d }
        return try? Date.ISO8601FormatStyle().parse(s)
    }

    public static func makeEncoder() -> JSONEncoder {
        let e = JSONEncoder()
        e.keyEncodingStrategy = .convertToSnakeCase
        return e
    }

    /// Encoder for the offline cache: snake_case keys and ISO-8601 dates, so `makeDecoder()` reads it back.
    public static func makeCacheEncoder() -> JSONEncoder {
        let e = makeEncoder(); e.dateEncodingStrategy = .iso8601; return e
    }

    // MARK: Core

    enum Method: String { case get = "GET", post = "POST", put = "PUT", patch = "PATCH", delete = "DELETE" }

    func url(_ path: String, query: [(String, String?)] = []) throws -> URL {
        var base = baseURL.absoluteString
        while base.hasSuffix("/") { base.removeLast() }
        guard var c = URLComponents(string: base + path) else { throw APIError.invalidURL }
        let items = query.compactMap { k, v in v.map { URLQueryItem(name: k, value: $0) } }
        if !items.isEmpty { c.queryItems = items }
        guard let u = c.url else { throw APIError.invalidURL }
        return u
    }

    /// Builds a request; mutating methods get an Idempotency-Key (caller-supplied for outbox replays).
    func request(_ method: Method, _ path: String, query: [(String, String?)] = [], body: (any Encodable & Sendable)? = nil,
                 idempotencyKey: String? = nil, authenticated: Bool = true) async throws -> URLRequest {
        var r = URLRequest(url: try url(path, query: query))
        r.httpMethod = method.rawValue
        r.setValue("application/json", forHTTPHeaderField: "Accept")
        if authenticated {
            guard let t = await tokens.token() else { throw APIError.notAuthenticated }
            r.setValue("Bearer \(t)", forHTTPHeaderField: "Authorization")
        }
        if method != .get {
            r.setValue(idempotencyKey ?? makeKey(), forHTTPHeaderField: "Idempotency-Key")
        }
        if let body {
            r.httpBody = try Self.makeEncoder().encode(body)
            r.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        return r
    }

    func perform(_ r: URLRequest, delegate: (any URLSessionTaskDelegate)? = nil) async throws -> (Data, HTTPURLResponse) {
        let data: Data, resp: URLResponse
        do { (data, resp) = try await session.data(for: r, delegate: delegate) }
        catch { throw APIError.transport((error as NSError).localizedDescription) }
        guard let http = resp as? HTTPURLResponse else { throw APIError.transport("non-HTTP response") }
        if http.statusCode == 401, let auth = r.value(forHTTPHeaderField: "Authorization"), auth.hasPrefix("Bearer ") {
            await tokenRejected(String(auth.dropFirst("Bearer ".count)))
        }
        return (data, http)
    }

    static func error(from data: Data, _ http: HTTPURLResponse) -> APIError {
        if let p = try? makeDecoder().decode(Problem.self, from: data) {
            let ra = http.value(forHTTPHeaderField: "Retry-After").flatMap(TimeInterval.init)
            return .problem(p, retryAfter: ra)
        }
        return .http(status: http.statusCode)
    }

    func send<T: Decodable>(_ method: Method, _ path: String, query: [(String, String?)] = [], body: (any Encodable & Sendable)? = nil,
                            idempotencyKey: String? = nil, authenticated: Bool = true, as: T.Type = T.self) async throws -> T {
        let r = try await request(method, path, query: query, body: body, idempotencyKey: idempotencyKey, authenticated: authenticated)
        let (data, http) = try await perform(r)
        guard (200..<300).contains(http.statusCode) else { throw Self.error(from: data, http) }
        do { return try Self.makeDecoder().decode(T.self, from: data) }
        catch { throw APIError.decoding(String(describing: error)) }
    }

    func sendNoContent(_ method: Method, _ path: String, query: [(String, String?)] = [], idempotencyKey: String? = nil) async throws {
        let r = try await request(method, path, query: query, idempotencyKey: idempotencyKey)
        let (data, http) = try await perform(r)
        guard (200..<300).contains(http.statusCode) else { throw Self.error(from: data, http) }
    }

    /// Request for the SSE stream; used by `SSEClient`.
    func eventStreamRequest() async throws -> URLRequest {
        var r = try await request(.get, "/v1/events")
        r.setValue("text/event-stream", forHTTPHeaderField: "Accept")
        r.timeoutInterval = 3600
        return r
    }
    var streamSession: URLSession { session }
    static func decodeError(_ data: Data, _ http: HTTPURLResponse) -> APIError { error(from: data, http) }

    // MARK: Auth

    /// Password sign-in (`POST /v1/auth/login`, D-012) against `server`: on success the device token goes to the token store (Keychain),
    /// this client switches to `server`, and the password is dropped. Failures are `LoginFailure`; a cancelled task throws `CancellationError`.
    @discardableResult
    public func login(server: URL, password: String, deviceName: String?, timeout: TimeInterval = 15) async throws -> LoginResponse {
        var r = URLRequest(url: try Self.loginURL(server))
        r.httpMethod = "POST"
        r.timeoutInterval = timeout
        r.setValue("application/json", forHTTPHeaderField: "Accept")
        r.setValue("application/json", forHTTPHeaderField: "Content-Type")
        r.httpBody = try Self.makeEncoder().encode(LoginRequest(password: password, deviceName: deviceName))
        let data: Data, resp: URLResponse
        do { (data, resp) = try await session.data(for: r) }
        catch { throw try Self.loginFailure(for: error, host: server.host ?? "") }
        try Task.checkCancellation()
        guard let http = resp as? HTTPURLResponse else { throw LoginFailure.unreachable }
        switch http.statusCode {
        case 200..<300:
            guard let ok = try? Self.makeDecoder().decode(LoginResponse.self, from: data), !ok.token.isEmpty else { throw LoginFailure.notInboxServer }
            try await tokens.save(ok.token)
            base.withLock { $0 = server }
            return ok
        case 401:
            if (try? Self.makeDecoder().decode(Problem.self, from: data))?.code == "invalid_credentials" { throw LoginFailure.invalidCredentials }
            throw LoginFailure.notInboxServer
        case 429:
            let seconds = http.value(forHTTPHeaderField: "Retry-After").flatMap { TimeInterval($0.trimmingCharacters(in: .whitespaces)) } ?? 5
            throw LoginFailure.rateLimited(retryAfter: max(seconds, 1))
        case 500...: throw LoginFailure.server
        default: throw LoginFailure.notInboxServer     // 404 and friends: the address answers, but it isn't the Inbox API
        }
    }

    static func loginURL(_ server: URL) throws -> URL {
        var base = server.absoluteString
        while base.hasSuffix("/") { base.removeLast() }
        guard let u = URL(string: base + "/v1/auth/login") else { throw APIError.invalidURL }
        return u
    }

    static func loginFailure(for error: Error, host: String) throws -> LoginFailure {
        if error is CancellationError { throw CancellationError() }
        guard let e = error as? URLError else { return .unreachable }
        switch e.code {
        case .cancelled: throw CancellationError()
        case .secureConnectionFailed, .serverCertificateUntrusted, .serverCertificateHasBadDate, .serverCertificateHasUnknownRoot,
             .serverCertificateNotYetValid, .clientCertificateRejected, .clientCertificateRequired, .appTransportSecurityRequiresSecureConnection:
            return .tls(host: host)
        default: return .unreachable
        }
    }

    public func revokeDevice(id: UUID) async throws { try await sendNoContent(.delete, "/v1/devices/\(id.uuidString.lowercased())") }

    // MARK: Businesses

    public func businesses() async throws -> [Business] { try await send(.get, "/v1/businesses", as: BusinessList.self).businesses }

    public func createBusiness(_ req: CreateBusinessRequest) async throws -> Business { try await send(.post, "/v1/businesses", body: req) }

    public func orderBusinesses(ids: [UUID]) async throws -> [Business] {
        struct Body: Encodable, Sendable { var ids: [UUID] }
        return try await send(.put, "/v1/businesses:order", body: Body(ids: ids), as: BusinessList.self).businesses
    }

    public func patchBusiness(id: UUID, _ req: PatchBusinessRequest) async throws -> Business {
        try await send(.patch, "/v1/businesses/\(id.uuidString.lowercased())", body: req)
    }

    public func deleteBusiness(id: UUID) async throws { try await sendNoContent(.delete, "/v1/businesses/\(id.uuidString.lowercased())") }

    // MARK: Messages

    public func messages(_ q: MessageListQuery = .init()) async throws -> MessagePage {
        try await send(.get, "/v1/messages", query: [
            ("business", q.business?.uuidString.lowercased()), ("unread", q.unread.map { $0 ? "true" : "false" }),
            ("to", q.to), ("limit", q.limit.map(String.init)), ("cursor", q.cursor),
        ])
    }

    public func batch(ids: [UUID], op: BatchOp, idempotencyKey: String? = nil) async throws -> [Message] {
        try await send(.post, "/v1/messages:batch", body: BatchRequest(ids: ids, op: op), idempotencyKey: idempotencyKey, as: BatchResponse.self).messages
    }

    public func message(id: UUID) async throws -> MessageDetail { try await send(.get, "/v1/messages/\(id.uuidString.lowercased())") }

    public func messageBody(id: UUID) async throws -> MessageBody { try await send(.get, "/v1/messages/\(id.uuidString.lowercased())/body") }

    public func messageRaw(id: UUID) async throws -> Data {
        let r = try await request(.get, "/v1/messages/\(id.uuidString.lowercased())/raw")
        let (data, http) = try await perform(r)
        guard (200..<300).contains(http.statusCode) else { throw Self.error(from: data, http) }
        return data
    }

    /// Resolves the 60 s presigned URL (the server answers 302 with `Location`); redirects are not followed.
    public func attachmentURL(id: UUID) async throws -> URL {
        let r = try await request(.get, "/v1/attachments/\(id.uuidString.lowercased())")
        let (data, http) = try await perform(r, delegate: NoRedirect())
        guard http.statusCode == 302 || http.statusCode == 301 || http.statusCode == 307,
              let loc = http.value(forHTTPHeaderField: "Location"), let u = URL(string: loc) else {
            throw (200..<300).contains(http.statusCode) ? APIError.decoding("missing Location") : Self.error(from: data, http)
        }
        return u
    }

    /// Downloads attachment bytes: resolves the presigned URL, then fetches it without the bearer token.
    public func attachmentData(id: UUID) async throws -> Data {
        let u = try await attachmentURL(id: id)
        let (data, http) = try await perform(URLRequest(url: u))
        guard (200..<300).contains(http.statusCode) else { throw Self.error(from: data, http) }
        return data
    }

    /// Asks the server to look for the message in Gmail again. Endpoint arrives with T19; callers handle 404.
    public func gmailRetry(id: UUID) async throws {
        try await sendNoContent(.post, "/v1/messages/\(id.uuidString.lowercased())/gmail:retry")
    }

    // MARK: Gmail

    public func gmailStatus() async throws -> GmailStatus { try await send(.get, "/v1/gmail/status") }
    public func gmailConnect() async throws -> GmailConnect { try await send(.post, "/v1/gmail/connect") }
    public func gmailDisconnect() async throws { try await sendNoContent(.post, "/v1/gmail:disconnect") }

    public func gmailLabels() async throws -> [GmailLabel] { try await send(.get, "/v1/gmail/labels", as: GmailLabelList.self).labels }
    public func createGmailLabel(_ name: String) async throws -> GmailLabel { try await send(.post, "/v1/gmail/labels", body: GmailLabelCreate(name: name)) }
    public func gmailFilters() async throws -> [GmailFilter] { try await send(.get, "/v1/gmail/filters", as: GmailFilterList.self).filters }
    /// Turns the chosen filters into label-only rules (T20).
    public func importFilters(ids: [String]) async throws -> ImportRulesResult {
        try await send(.post, "/v1/rules:import", body: ImportRulesBody(filterIds: ids))
    }

    // MARK: Cloudflare

    /// 503 `cloudflare_not_configured` when the server has no Cloudflare token.
    public func cloudflareDomains() async throws -> [CloudflareDomain] {
        try await send(.get, "/v1/cloudflare/domains", as: CloudflareDomainList.self).domains
    }
    /// Re-reads one zone's routing after the user fixed it in Cloudflare (OB03 "Check again").
    public func checkCloudflareDomain(zoneId: String) async throws -> CloudflareDomain {
        try await send(.post, "/v1/cloudflare/domains/\(zoneId):check")
    }

    // MARK: Health, routing, Alfather

    public func health() async throws -> HealthReport { try await send(.get, "/v1/health") }
    public func checkRouting(businessId: UUID) async throws -> RoutingCheck {
        try await send(.post, "/v1/businesses/\(businessId.uuidString.lowercased())/routing:check")
    }
    /// `nil` when the server has no Alfather endpoint yet (404/501).
    public func alfatherStatus() async throws -> AlfatherStatus? {
        do { return try await send(.get, "/v1/alfather/status") as AlfatherStatus }
        catch let e as APIError {
            if case .http(let s) = e, s == 404 || s == 501 { return nil }
            if case .problem(let p, _) = e, p.status == 404 || p.status == 501 { return nil }
            throw e
        }
    }

    // MARK: Backfill

    public func backfillEstimate(days: Int? = nil, since: String? = nil, businesses: [UUID]? = nil) async throws -> BackfillEstimate {
        try await send(.get, "/v1/backfills:estimate", query: [("days", days.map(String.init)), ("since", since),
                                                                ("businesses", businesses.map { $0.map { $0.uuidString.lowercased() }.joined(separator: ",") })])
    }
    public func backfills() async throws -> [Backfill] { try await send(.get, "/v1/backfills", as: BackfillList.self).backfills }
    public func startBackfill(days: Int? = nil, since: String? = nil, businesses: [UUID]? = nil) async throws -> [Backfill] {
        try await send(.post, "/v1/backfills", body: BackfillCreate(days: days, since: since, businessIds: businesses), as: BackfillList.self).backfills
    }
    public func pauseBackfill(id: UUID) async throws -> Backfill { try await send(.post, "/v1/backfills/\(id.uuidString.lowercased()):pause") }
    public func resumeBackfill(id: UUID) async throws -> Backfill { try await send(.post, "/v1/backfills/\(id.uuidString.lowercased()):resume") }
    public func cancelBackfill(id: UUID) async throws -> Backfill { try await send(.post, "/v1/backfills/\(id.uuidString.lowercased()):cancel") }

    // MARK: Prefs and data

    public func prefs() async throws -> Prefs { try await send(.get, "/v1/prefs") }
    @discardableResult
    public func updatePrefs(_ patch: PrefsPatch) async throws -> Prefs { try await send(.put, "/v1/prefs", body: patch) }
    /// Step 1 of "Delete everything": a short-lived token the person types to confirm.
    public func confirmDataDeletion() async throws -> DataConfirmation { try await send(.post, "/v1/data:confirm") }
    public func deleteAllData(confirm: String) async throws -> DataDeletionResult {
        try await send(.delete, "/v1/data", body: DeleteDataBody(confirm: confirm))
    }

    /// Creates (or replaces) the pairing; the response carries the one-time token for Alfather.
    public func createAlfatherPairing(includeExisting: Bool = false) async throws -> AlfatherPairingGrant {
        try await send(.post, "/v1/alfather/pairings", body: AlfatherPairingBody(includeExisting: includeExisting))
    }
    public func unpairAlfather() async throws { try await sendNoContent(.delete, "/v1/alfather") }

    // MARK: Search

    /// `limit` 1...200 (server default 100), `cursor` from the previous page's `next_cursor` (same `q` and `business`).
    public func search(_ q: String, business: UUID? = nil, limit: Int? = nil, cursor: String? = nil) async throws -> SearchResponse {
        try await send(.get, "/v1/search", query: [("q", q), ("business", business?.uuidString.lowercased()), ("limit", limit.map(String.init)), ("cursor", cursor)])
    }

    public func recentSearches() async throws -> [String] { try await send(.get, "/v1/search/recents", as: Recents.self).recents }

    public func addRecentSearch(_ q: String) async throws -> [String] {
        struct Body: Encodable, Sendable { var q: String }
        return try await send(.post, "/v1/search/recents", body: Body(q: q), as: Recents.self).recents
    }

    /// Clears all recents, or only the entry matching `q`.
    public func deleteRecentSearches(q: String? = nil) async throws { try await sendNoContent(.delete, "/v1/search/recents", query: [("q", q)]) }

    // MARK: Rules

    private func rid(_ id: UUID) -> String { id.uuidString.lowercased() }

    public func rules() async throws -> [Rule] { try await send(.get, "/v1/rules", as: RuleList.self).rules }
    public func createRule(_ r: RuleInput, idempotencyKey: String? = nil) async throws -> Rule {
        try await send(.post, "/v1/rules", body: r, idempotencyKey: idempotencyKey)
    }
    /// Full edit: pass the whole draft (nil conditions are sent as null and cleared).
    public func patchRule(id: UUID, _ r: RuleInput) async throws -> Rule { try await send(.patch, "/v1/rules/\(rid(id))", body: r) }
    public func patchRule(id: UUID, _ p: RulePatch) async throws -> Rule { try await send(.patch, "/v1/rules/\(rid(id))", body: p) }
    public func deleteRule(id: UUID) async throws { try await sendNoContent(.delete, "/v1/rules/\(rid(id))") }
    public func orderRules(ids: [UUID]) async throws -> [Rule] {
        try await send(.put, "/v1/rules:order", body: RuleOrderBody(ids: ids), as: RuleList.self).rules
    }
    public func testRule(_ r: RuleInput, scope: RuleTestScope = .all, limit: Int = 200) async throws -> RuleTestResult {
        try await send(.post, "/v1/rules:test", body: RuleTestBody(rule: r, scope: scope, limit: limit))
    }
    public func applyRule(id: UUID, scope: RuleTestScope = .unrouted) async throws -> ApplyRuleResult {
        try await send(.post, "/v1/rules/\(rid(id)):apply", body: ApplyBody(scope: scope))
    }

    // MARK: Attention

    public func attention() async throws -> AttentionSummary { try await send(.get, "/v1/attention") }
    public func unrouted() async throws -> UnroutedList { try await send(.get, "/v1/attention/unrouted") }
    public func failed() async throws -> [FailedMessage] { try await send(.get, "/v1/attention/failed", as: FailedList.self).messages }

    public func retryFailed(id: UUID) async throws -> RetryResponse {
        try await send(.post, "/v1/failed/\(id.uuidString.lowercased()):retry")
    }

    public func dismissFailed(id: UUID) async throws { try await sendNoContent(.post, "/v1/failed/\(id.uuidString.lowercased()):dismiss") }
}

private final class NoRedirect: NSObject, URLSessionTaskDelegate {
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
        completionHandler(nil)
    }
}
