import Foundation

// Hand-written Codable models mirroring api/openapi.yaml. Wire format is snake_case; the
// client's JSONDecoder/JSONEncoder convert keys, so properties are plain camelCase.

public struct Address: Codable, Sendable, Hashable {
    public var localPart: String
    public var count: Int
    public var lastAt: Date?
}

public struct Business: Codable, Sendable, Hashable, Identifiable {
    public var id: UUID
    public var name: String
    public var domain: String
    public var sortOrder: Int
    public var hidden: Bool
    public var enabled: Bool
    public var routing: String
    public var unread: Int
    public var lastMessageAt: Date?
    public var addresses: [Address]
}

public struct BusinessList: Codable, Sendable { public var businesses: [Business] }

public struct CreateBusinessRequest: Codable, Sendable {
    public var name: String
    public var domain: String
    public var cfZoneId: String?
    public init(name: String, domain: String, cfZoneId: String? = nil) {
        self.name = name; self.domain = domain; self.cfZoneId = cfZoneId
    }
}

public struct PatchBusinessRequest: Codable, Sendable {
    public var name: String?
    public var hidden: Bool?
    public var enabled: Bool?
    public init(name: String? = nil, hidden: Bool? = nil, enabled: Bool? = nil) {
        self.name = name; self.hidden = hidden; self.enabled = enabled
    }
}

/// `POST /v1/auth/login` body. The password only lives in this value for the duration of the request; it is never stored.
public struct LoginRequest: Codable, Sendable {
    public var password: String
    public var deviceName: String?
    public init(password: String, deviceName: String? = nil) { self.password = password; self.deviceName = deviceName }
}

public struct LoginResponse: Codable, Sendable {
    public var deviceId: UUID
    public var token: String
}

public struct Message: Codable, Sendable, Hashable, Identifiable {
    public var id: UUID
    public var businessId: UUID?
    public var direction: String
    public var fromAddr: String?
    public var fromName: String?
    public var subject: String?
    public var snippet: String?
    public var toLocal: String?
    public var toTag: String?
    public var receivedAt: Date
    public var isRead: Bool
    public var archived: Bool
    public var attachmentCount: Int
    public var threadKey: String
    public var route: String
}

public struct MessagePage: Codable, Sendable {
    public var messages: [Message]
    public var nextCursor: String?
}

public struct MessageListQuery: Sendable, Hashable {
    public var business: UUID?
    public var unread: Bool?
    public var to: String?
    public var limit: Int?
    public var cursor: String?
    public init(business: UUID? = nil, unread: Bool? = nil, to: String? = nil, limit: Int? = nil, cursor: String? = nil) {
        self.business = business; self.unread = unread; self.to = to; self.limit = limit; self.cursor = cursor
    }
}

public enum BatchOp: String, Codable, Sendable, CaseIterable {
    case read, unread, archive, unarchive
    public var inverse: BatchOp {
        switch self {
        case .read: .unread
        case .unread: .read
        case .archive: .unarchive
        case .unarchive: .archive
        }
    }
}

public struct BatchRequest: Codable, Sendable {
    public var ids: [UUID]
    public var op: BatchOp
}

public struct BatchResponse: Codable, Sendable { public var messages: [Message] }

public struct Attachment: Codable, Sendable, Hashable, Identifiable {
    public var id: UUID
    public var filename: String?
    public var mime: String?
    public var sizeBytes: Int?
    public var contentId: String?
    public var inline: Bool
}

public struct BusinessRef: Codable, Sendable, Hashable {
    public var id: UUID
    public var name: String
    public var domain: String?
}

public struct MessageDetail: Codable, Sendable {
    public struct GmailState: Codable, Sendable, Hashable {
        /// `synced`, `pending`, `not_found`, `error` or `signed_out` (server enum not final until T19).
        public var state: String
        public var lastError: String?
        public var attempts: Int?
        public var lastAttemptAt: Date?
        public var lastSyncedAt: Date?
    }
    public struct Details: Codable, Sendable {
        public var business: BusinessRef?
        public var address: String?
        public var ruleName: String?
        /// Gmail message id for "Open in Gmail" deep links; the server doesn't send it yet (Q-T14-2).
        public var gmailId: String?
        public var gmail: GmailState?
        public var labels: [String]
        public var sizeBytes: Int?
        public var receivedAt: Date
    }
    public var message: Message
    public var thread: [Message]
    public var attachments: [Attachment]
    public var details: Details
}

public struct MessageBody: Codable, Sendable, Hashable {
    public struct AuthResults: Codable, Sendable, Hashable { public var spf: String; public var dkim: String; public var dmarc: String }
    public struct Inline: Codable, Sendable, Hashable { public var cid: String; public var attachmentId: UUID?; public var mime: String }
    public var html: String?
    public var text: String?
    public var remoteImageCount: Int
    public var authResults: AuthResults
    public var inline: [Inline]
}

public struct Span: Codable, Sendable, Hashable {
    public var start: Int; public var end: Int
    public init(start: Int, end: Int) { self.start = start; self.end = end }
}

public struct SearchResult: Codable, Sendable, Hashable, Identifiable {
    public struct Highlights: Codable, Sendable, Hashable { public var subject: [Span]; public var from: [Span]; public var snippet: [Span] }
    public var id: UUID
    public var businessId: UUID?
    public var fromAddr: String?
    public var fromName: String?
    public var subject: String?
    public var snippet: String?
    public var receivedAt: Date
    public var isRead: Bool
    public var archived: Bool
    public var attachmentCount: Int
    public var threadKey: String
    public var highlights: Highlights
}

public struct SearchGroup: Codable, Sendable { public var business: BusinessRef?; public var total: Int; public var messages: [SearchResult] }

public struct SearchResponse: Codable, Sendable {
    public struct Parsed: Codable, Sendable {
        public var words: [String]; public var phrases: [String]; public var prefix: String
        public var from: [String]; public var to: [String]; public var hasAttachment: Bool; public var terms: [String]
    }
    public var groups: [SearchGroup]
    /// Results on this page (B-4).
    public var total: Int
    public var parsed: Parsed
    public var hasMore: Bool = false
    public var nextCursor: String?

    private enum CodingKeys: String, CodingKey { case groups, total, parsed, hasMore, nextCursor }
    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        groups = try c.decode([SearchGroup].self, forKey: .groups); total = try c.decode(Int.self, forKey: .total)
        parsed = try c.decode(Parsed.self, forKey: .parsed)
        hasMore = try c.decodeIfPresent(Bool.self, forKey: .hasMore) ?? false
        nextCursor = try c.decodeIfPresent(String.self, forKey: .nextCursor)
    }
}

public struct Recents: Codable, Sendable { public var recents: [String] }

public struct AttentionSummary: Codable, Sendable {
    public struct Unrouted: Codable, Sendable {
        public struct Entry: Codable, Sendable, Hashable { public var businessId: UUID?; public var name: String?; public var count: Int }
        public var count: Int
        public var byBusiness: [Entry]
    }
    public struct Failed: Codable, Sendable { public var count: Int }
    public var unrouted: Unrouted
    public var failed: Failed
    public var total: Int
}

public struct UnroutedList: Codable, Sendable {
    public struct Group: Codable, Sendable { public var business: BusinessRef?; public var count: Int; public var messages: [Message] }
    public var groups: [Group]
    public var total: Int
}

/// `FailedMessage` = Message fields plus failure info (OpenAPI allOf).
public struct FailedMessage: Codable, Sendable, Hashable, Identifiable {
    public var message: Message
    public var failureReason: String?
    public var envTo: String?
    public var sizeBytes: Int?
    public var hasRaw: Bool
    public var id: UUID { message.id }

    private enum Extra: String, CodingKey { case failureReason, envTo, sizeBytes, hasRaw }
    public init(from decoder: Decoder) throws {
        message = try Message(from: decoder)
        let c = try decoder.container(keyedBy: Extra.self)
        failureReason = try c.decodeIfPresent(String.self, forKey: .failureReason)
        envTo = try c.decodeIfPresent(String.self, forKey: .envTo)
        sizeBytes = try c.decodeIfPresent(Int.self, forKey: .sizeBytes)
        hasRaw = try c.decode(Bool.self, forKey: .hasRaw)
    }
    public func encode(to encoder: Encoder) throws {
        try message.encode(to: encoder)
        var c = encoder.container(keyedBy: Extra.self)
        try c.encode(failureReason, forKey: .failureReason)
        try c.encode(envTo, forKey: .envTo)
        try c.encode(sizeBytes, forKey: .sizeBytes)
        try c.encode(hasRaw, forKey: .hasRaw)
    }
}

public struct FailedList: Codable, Sendable { public var messages: [FailedMessage] }
public struct RetryResponse: Codable, Sendable { public var status: String; public var id: UUID }

// MARK: Rules

public struct Rule: Codable, Sendable, Hashable, Identifiable {
    public var id: UUID
    public var name: String
    public var position: Int
    public var enabled: Bool
    public var businessId: UUID?
    public var matchToLocal: String?
    public var matchFrom: String?
    public var matchSubject: String?
    public var gmailLabel: String?
    public var gmailArchive: Bool
    public var gmailMarkRead: Bool
    public var labelOnly: Bool
    public var importedFromFilter: String?
    public var messageCount: Int
    public var createdAt: Date
    public var updatedAt: Date
}

public struct RuleList: Codable, Sendable { public var rules: [Rule] }

/// Create / edit / test body. Every field is always encoded (nil as `null`) so PATCH clears a removed condition.
public struct RuleInput: Codable, Sendable, Hashable {
    public var id: UUID?
    public var name: String
    public var enabled: Bool
    public var businessId: UUID?
    public var matchToLocal: String?
    public var matchFrom: String?
    public var matchSubject: String?
    public var gmailLabel: String?
    public var gmailArchive: Bool
    public var gmailMarkRead: Bool
    public var labelOnly: Bool

    public init(id: UUID? = nil, name: String, enabled: Bool = true, businessId: UUID? = nil, matchToLocal: String? = nil,
                matchFrom: String? = nil, matchSubject: String? = nil, gmailLabel: String? = nil,
                gmailArchive: Bool = false, gmailMarkRead: Bool = false, labelOnly: Bool = true) {
        self.id = id; self.name = name; self.enabled = enabled; self.businessId = businessId; self.matchToLocal = matchToLocal
        self.matchFrom = matchFrom; self.matchSubject = matchSubject; self.gmailLabel = gmailLabel
        self.gmailArchive = gmailArchive; self.gmailMarkRead = gmailMarkRead; self.labelOnly = labelOnly
    }

    private enum Keys: String, CodingKey {
        case id, name, enabled, businessId, matchToLocal, matchFrom, matchSubject, gmailLabel, gmailArchive, gmailMarkRead, labelOnly
    }
    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: Keys.self)
        try c.encodeIfPresent(id, forKey: .id)
        try c.encode(name, forKey: .name)
        try c.encode(enabled, forKey: .enabled)
        try c.encode(businessId, forKey: .businessId)
        try c.encode(matchToLocal, forKey: .matchToLocal)
        try c.encode(matchFrom, forKey: .matchFrom)
        try c.encode(matchSubject, forKey: .matchSubject)
        try c.encode(gmailLabel, forKey: .gmailLabel)
        try c.encode(gmailArchive, forKey: .gmailArchive)
        try c.encode(gmailMarkRead, forKey: .gmailMarkRead)
        try c.encode(labelOnly, forKey: .labelOnly)
    }
}

/// Partial update used by the list's switch (only `enabled`).
public struct RulePatch: Codable, Sendable, Hashable {
    public var enabled: Bool?
    public init(enabled: Bool? = nil) { self.enabled = enabled }
}

public enum RuleTestScope: String, Codable, Sendable { case all, unrouted }

public struct RuleMessageRef: Codable, Sendable, Hashable, Identifiable {
    public var id: UUID
    public var businessId: UUID?
    public var toLocal: String?
    public var fromAddr: String?
    public var fromName: String?
    public var subject: String?
    public var receivedAt: Date
}

public struct RuleTestResult: Codable, Sendable, Hashable {
    public struct Earlier: Codable, Sendable, Hashable, Identifiable {
        public struct ByRule: Codable, Sendable, Hashable { public var id: UUID; public var name: String }
        public var message: RuleMessageRef
        public var byRule: ByRule
        public var id: UUID { message.id }
    }
    public var scope: RuleTestScope
    public var scanned: Int
    public var wouldMatch: [RuleMessageRef]
    public var caughtEarlier: [Earlier]
}

struct RuleOrderBody: Codable, Sendable { var ids: [UUID] }
struct RuleTestBody: Codable, Sendable { var rule: RuleInput; var scope: RuleTestScope; var limit: Int }
struct ApplyBody: Codable, Sendable { var scope: RuleTestScope }
public struct ApplyRuleResult: Codable, Sendable { public var scope: RuleTestScope; public var routed: Int }

// MARK: Gmail

public struct GmailStatus: Codable, Sendable, Hashable {
    public enum State: String, Codable, Sendable { case notConnected = "not_connected", connected, needsReauth = "needs_reauth" }
    public var state: State
    public var email: String?
    public var connectedAt: Date?
    public var lastWriteAt: Date?
    public var waiting: Int
    public var rateLimitedUntil: Date?
    public var configured: Bool

    public init(state: State, email: String? = nil, connectedAt: Date? = nil, lastWriteAt: Date? = nil, waiting: Int = 0,
                rateLimitedUntil: Date? = nil, configured: Bool = true) {
        self.state = state; self.email = email; self.connectedAt = connectedAt; self.lastWriteAt = lastWriteAt
        self.waiting = waiting; self.rateLimitedUntil = rateLimitedUntil; self.configured = configured
    }

    private enum CodingKeys: String, CodingKey { case state, email, connectedAt, lastWriteAt, waiting, rateLimitedUntil, configured }
    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        state = try c.decode(State.self, forKey: .state)
        email = try c.decodeIfPresent(String.self, forKey: .email)
        connectedAt = try c.decodeIfPresent(Date.self, forKey: .connectedAt)
        lastWriteAt = try c.decodeIfPresent(Date.self, forKey: .lastWriteAt)
        waiting = try c.decodeIfPresent(Int.self, forKey: .waiting) ?? 0
        rateLimitedUntil = try c.decodeIfPresent(Date.self, forKey: .rateLimitedUntil)
        configured = try c.decodeIfPresent(Bool.self, forKey: .configured) ?? true
    }
}

public struct GmailConnect: Codable, Sendable, Hashable {
    public var authUrl: URL
    public var callbackScheme: String
    public var expiresIn: Int
}

public struct GmailLabel: Codable, Sendable, Hashable, Identifiable { public var id: String; public var name: String }
struct GmailLabelList: Codable, Sendable { var labels: [GmailLabel] }
struct GmailLabelCreate: Codable, Sendable { var name: String }

/// One Gmail filter as `GET /v1/gmail/filters` lists it (T20).
public struct GmailFilter: Codable, Sendable, Hashable, Identifiable {
    public struct ProposedRule: Codable, Sendable, Hashable { public var name: String; public var gmailLabel: String? }
    public var id: String
    public var from: String?
    public var to: String?
    public var subject: String?
    public var label: String?
    public var archive: Bool
    public var markRead: Bool
    public var delete: Bool
    public var importable: Bool
    public var reason: String?          // deletes_mail | forwards_mail | no_label | unsupported_criteria | unmanaged_domain
    public var alreadyImported: Bool
    public var rule: ProposedRule?

    /// Gmail search form: "from:(stripe.com) to:(invoices@x.com)".
    public var summary: String {
        [from.map { "from:(\($0))" }, to.map { "to:(\($0))" }, subject.map { "subject:(\($0))" }].compactMap { $0 }.joined(separator: " ")
    }
    public var title: String { rule?.name ?? label ?? summary }

    private enum CodingKeys: String, CodingKey { case id, from, to, subject, label, archive, markRead, delete, importable, reason, alreadyImported, rule }
    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        from = try c.decodeIfPresent(String.self, forKey: .from); to = try c.decodeIfPresent(String.self, forKey: .to)
        subject = try c.decodeIfPresent(String.self, forKey: .subject); label = try c.decodeIfPresent(String.self, forKey: .label)
        archive = try c.decodeIfPresent(Bool.self, forKey: .archive) ?? false
        markRead = try c.decodeIfPresent(Bool.self, forKey: .markRead) ?? false
        delete = try c.decodeIfPresent(Bool.self, forKey: .delete) ?? false
        importable = try c.decode(Bool.self, forKey: .importable)
        reason = try c.decodeIfPresent(String.self, forKey: .reason)
        alreadyImported = try c.decodeIfPresent(Bool.self, forKey: .alreadyImported) ?? false
        rule = try c.decodeIfPresent(ProposedRule.self, forKey: .rule)
    }
}
struct GmailFilterList: Codable, Sendable { var filters: [GmailFilter] }
public struct ImportRulesResult: Codable, Sendable {
    public struct Imported: Codable, Sendable, Hashable { public var filterId: String; public var ruleId: UUID }
    public struct Skipped: Codable, Sendable, Hashable { public var filterId: String; public var reason: String }
    public var imported: [Imported]
    public var skipped: [Skipped]
}
struct ImportRulesBody: Codable, Sendable { var filterIds: [String] }

// MARK: Cloudflare

public struct CloudflareDomain: Codable, Sendable, Hashable, Identifiable {
    public enum Routing: String, Codable, Sendable { case ok, toEmail = "to_email", none, unknown }
    public var zoneId: String
    public var domain: String
    public var status: String
    public var routing: Routing
    public var alreadyAdded: Bool
    public var selectable: Bool
    public var id: String { zoneId }
    public init(zoneId: String, domain: String, status: String = "active", routing: Routing, alreadyAdded: Bool = false, selectable: Bool) {
        self.zoneId = zoneId; self.domain = domain; self.status = status; self.routing = routing
        self.alreadyAdded = alreadyAdded; self.selectable = selectable
    }
    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        zoneId = try c.decode(String.self, forKey: .zoneId); domain = try c.decode(String.self, forKey: .domain)
        status = try c.decodeIfPresent(String.self, forKey: .status) ?? "active"
        routing = (try? c.decode(Routing.self, forKey: .routing)) ?? .unknown
        alreadyAdded = try c.decodeIfPresent(Bool.self, forKey: .alreadyAdded) ?? false
        selectable = try c.decodeIfPresent(Bool.self, forKey: .selectable) ?? false
    }
    private enum CodingKeys: String, CodingKey { case zoneId, domain, status, routing, alreadyAdded, selectable }
}
struct CloudflareDomainList: Codable, Sendable { var domains: [CloudflareDomain] }

// MARK: Health, routing, Alfather

public struct HealthCheck: Codable, Sendable, Hashable, Identifiable {
    public enum State: String, Codable, Sendable { case ok, warn, error, off }
    public var id: String
    public var businessId: UUID?
    public var title: String
    public var state: State
    public var message: String
    public var fixRoute: String?
    /// `mail_arriving` has one check per business, so the id alone isn't unique.
    public var key: String { businessId.map { "\(id)-\($0.uuidString)" } ?? id }
    public init(id: String, businessId: UUID? = nil, title: String, state: State, message: String, fixRoute: String? = nil) {
        self.id = id; self.businessId = businessId; self.title = title; self.state = state; self.message = message; self.fixRoute = fixRoute
    }
    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id); businessId = try c.decodeIfPresent(UUID.self, forKey: .businessId)
        title = try c.decode(String.self, forKey: .title); state = (try? c.decode(State.self, forKey: .state)) ?? .off
        message = try c.decodeIfPresent(String.self, forKey: .message) ?? ""; fixRoute = try c.decodeIfPresent(String.self, forKey: .fixRoute)
    }
    private enum CodingKeys: String, CodingKey { case id, businessId, title, state, message, fixRoute }
}

public struct HealthReport: Codable, Sendable, Hashable {
    public enum Overall: String, Codable, Sendable { case ok, needsYou = "needs_you" }
    public var state: Overall
    public var checkedAt: Date
    public var checks: [HealthCheck]
    public init(state: Overall, checkedAt: Date, checks: [HealthCheck]) { self.state = state; self.checkedAt = checkedAt; self.checks = checks }
    /// Checks that are warn or error (`off` never counts).
    public var problems: [HealthCheck] { checks.filter { $0.state == .warn || $0.state == .error } }
}

public struct RoutingCheck: Codable, Sendable, Hashable {
    public var businessId: UUID
    public var routing: CloudflareDomain.Routing
    public var checkedAt: Date
}

/// `GET /v1/alfather/status` (T31): paired / connected (an authenticated MCP call in the last 5 minutes) / last seen / backlog.
public struct AlfatherStatus: Codable, Sendable, Hashable {
    public enum State: String, Sendable { case notPaired = "not_paired", connected, reconnecting, unreachable }
    public var paired: Bool
    public var connected: Bool
    public var pairedAt: Date?
    public var lastSeen: Date?
    public var waiting: Int
    public var mcpUrl: String?

    public init(paired: Bool, connected: Bool, pairedAt: Date? = nil, lastSeen: Date? = nil, waiting: Int = 0, mcpUrl: String? = nil) {
        self.paired = paired; self.connected = connected; self.pairedAt = pairedAt; self.lastSeen = lastSeen; self.waiting = waiting; self.mcpUrl = mcpUrl
    }
    public init(state: State, lastSeen: Date? = nil, waiting: Int = 0) {
        self.init(paired: state != .notPaired, connected: state == .connected, lastSeen: lastSeen, waiting: waiting)
    }
    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        paired = try c.decodeIfPresent(Bool.self, forKey: .paired) ?? false
        connected = try c.decodeIfPresent(Bool.self, forKey: .connected) ?? false
        pairedAt = try c.decodeIfPresent(Date.self, forKey: .pairedAt)
        lastSeen = try c.decodeIfPresent(Date.self, forKey: .lastSeenAt)
        waiting = try c.decodeIfPresent(Int.self, forKey: .waiting) ?? 0
        mcpUrl = try c.decodeIfPresent(String.self, forKey: .mcpUrl)
    }
    public func encode(to e: Encoder) throws {
        var c = e.container(keyedBy: CodingKeys.self)
        try c.encode(paired, forKey: .paired); try c.encode(connected, forKey: .connected)
        try c.encodeIfPresent(pairedAt, forKey: .pairedAt); try c.encodeIfPresent(lastSeen, forKey: .lastSeenAt)
        try c.encode(waiting, forKey: .waiting); try c.encodeIfPresent(mcpUrl, forKey: .mcpUrl)
    }
    private enum CodingKeys: String, CodingKey { case paired, connected, pairedAt, lastSeenAt, waiting, mcpUrl }

    /// AF01–AF04: not paired → setup; connected → ok; paired but quiet for under 15 min → reconnecting; longer → not reachable.
    public func state(now: Date = .now) -> State {
        if !paired { return .notPaired }
        if connected { return .connected }
        if let t = lastSeen, now.timeIntervalSince(t) < 15 * 60 { return .reconnecting }
        return .unreachable
    }
}

/// `POST /v1/alfather/pairings`: the credentials handed to Alfather over the LAN. The token is shown once; never store or log it.
public struct AlfatherPairingGrant: Codable, Sendable {
    public var consumerId: String
    public var mcpUrl: String
    public var token: String
    public var scope: String
}
struct AlfatherPairingBody: Codable, Sendable { var includeExisting: Bool }

// MARK: Backfill (import older mail)

public struct Backfill: Codable, Sendable, Hashable, Identifiable {
    public enum Status: String, Codable, Sendable { case queued, running, paused, done, failed, cancelled }
    public var id: UUID
    public var businessId: UUID
    public var businessName: String
    public var domain: String
    public var since: String
    public var status: Status
    public var estimated: Int
    public var scanned: Int
    public var imported: Int
    public var sorted: Int
    public var unrouted: Int
    public var skipped: Int
    public var pausedUntil: Date?
    public var pauseReason: String?       // rate_limited | gmail_signed_out | user
    public var lastError: String?
    public var createdAt: Date?
    public var startedAt: Date?
    public var finishedAt: Date?

    public var isActive: Bool { status == .queued || status == .running || status == .paused }

    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        id = try c.decode(UUID.self, forKey: .id); businessId = try c.decode(UUID.self, forKey: .businessId)
        businessName = try c.decodeIfPresent(String.self, forKey: .businessName) ?? ""; domain = try c.decodeIfPresent(String.self, forKey: .domain) ?? ""
        since = try c.decodeIfPresent(String.self, forKey: .since) ?? ""
        status = (try? c.decode(Status.self, forKey: .status)) ?? .queued
        estimated = try c.decodeIfPresent(Int.self, forKey: .estimated) ?? 0; scanned = try c.decodeIfPresent(Int.self, forKey: .scanned) ?? 0
        imported = try c.decodeIfPresent(Int.self, forKey: .imported) ?? 0; sorted = try c.decodeIfPresent(Int.self, forKey: .sorted) ?? 0
        unrouted = try c.decodeIfPresent(Int.self, forKey: .unrouted) ?? 0; skipped = try c.decodeIfPresent(Int.self, forKey: .skipped) ?? 0
        pausedUntil = try c.decodeIfPresent(Date.self, forKey: .pausedUntil); pauseReason = try c.decodeIfPresent(String.self, forKey: .pauseReason)
        lastError = try c.decodeIfPresent(String.self, forKey: .lastError); createdAt = try c.decodeIfPresent(Date.self, forKey: .createdAt)
        startedAt = try c.decodeIfPresent(Date.self, forKey: .startedAt); finishedAt = try c.decodeIfPresent(Date.self, forKey: .finishedAt)
    }
    private enum CodingKeys: String, CodingKey {
        case id, businessId, businessName, domain, since, status, estimated, scanned, imported, sorted, unrouted, skipped
        case pausedUntil, pauseReason, lastError, createdAt, startedAt, finishedAt
    }
}
struct BackfillList: Codable, Sendable { var backfills: [Backfill] }

public struct BackfillEstimate: Codable, Sendable, Hashable {
    public struct Entry: Codable, Sendable, Hashable { public var businessId: UUID; public var name: String; public var domain: String; public var estimate: Int }
    public var since: String
    public var total: Int
    public var etaSeconds: Int?
    public var businesses: [Entry]
}
struct BackfillCreate: Codable, Sendable { var days: Int?; var since: String?; var businessIds: [UUID]? }

// MARK: Prefs and data deletion (T27)

public struct Prefs: Codable, Sendable, Hashable {
    /// nil = keep forever; otherwise 7...3650 days.
    public var retentionDays: Int?
    public var imageAllowSenders: [String]
    public var previewLines: Int
    public init(retentionDays: Int? = nil, imageAllowSenders: [String] = [], previewLines: Int = 2) {
        self.retentionDays = retentionDays; self.imageAllowSenders = imageAllowSenders; self.previewLines = previewLines
    }
    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        retentionDays = try c.decodeIfPresent(Int.self, forKey: .retentionDays)
        imageAllowSenders = try c.decodeIfPresent([String].self, forKey: .imageAllowSenders) ?? []
        previewLines = try c.decodeIfPresent(Int.self, forKey: .previewLines) ?? 2
    }
    private enum CodingKeys: String, CodingKey { case retentionDays, imageAllowSenders, previewLines }
}

/// Partial `PUT /v1/prefs` body: only the fields set here are encoded; `.keepForever` encodes `retention_days: null`.
public struct PrefsPatch: Encodable, Sendable {
    public enum Retention: Sendable { case days(Int), keepForever }
    public var retention: Retention?
    public var imageAllowSenders: [String]?
    public var previewLines: Int?
    public init(retention: Retention? = nil, imageAllowSenders: [String]? = nil, previewLines: Int? = nil) {
        self.retention = retention; self.imageAllowSenders = imageAllowSenders; self.previewLines = previewLines
    }
    private enum CodingKeys: String, CodingKey { case retentionDays, imageAllowSenders, previewLines }
    public func encode(to e: Encoder) throws {
        var c = e.container(keyedBy: CodingKeys.self)
        switch retention {
        case .days(let d)?: try c.encode(d, forKey: .retentionDays)
        case .keepForever?: try c.encodeNil(forKey: .retentionDays)
        case nil: break
        }
        try c.encodeIfPresent(imageAllowSenders, forKey: .imageAllowSenders)
        try c.encodeIfPresent(previewLines, forKey: .previewLines)
    }
}

public struct DataConfirmation: Codable, Sendable, Hashable { public var token: String; public var expiresAt: Date?; public var expiresIn: Int? }
public struct DataDeletionResult: Codable, Sendable, Hashable { public var deletedMessages: Int; public var deletedBlobs: Int }
struct DeleteDataBody: Codable, Sendable { var confirm: String }
