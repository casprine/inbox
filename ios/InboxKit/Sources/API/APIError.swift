import Foundation

/// Server problem+json body (RFC 9457 subset used by the Inbox server).
public struct Problem: Codable, Sendable, Hashable {
    public var type: String
    public var title: String
    public var status: Int
    public var code: String
    public var detail: String?
    /// Rule validation (422): the first offending field, its 1-based column, and every problem found.
    public var field: String?
    public var column: Int?
    public var errors: [RuleFieldError]?
}

public struct RuleFieldError: Codable, Sendable, Hashable {
    public var field: String
    public var message: String
    public var column: Int?
    public init(field: String, message: String, column: Int? = nil) { self.field = field; self.message = message; self.column = column }
}

public enum APIError: Error, Sendable, Equatable {
    /// Server answered with a problem+json body.
    case problem(Problem, retryAfter: TimeInterval?)
    /// Non-2xx without a parseable problem body.
    case http(status: Int)
    case transport(String)
    case decoding(String)
    case invalidURL
    case notAuthenticated

    public var code: String? {
        if case .problem(let p, _) = self { return p.code }
        if case .notAuthenticated = self { return "unauthorized" }
        return nil
    }
    public var status: Int? {
        switch self {
        case .problem(let p, _): p.status
        case .http(let s): s
        default: nil
        }
    }
    /// Field errors of a 422 `rule_pattern_invalid` / `invalid_rule` answer (empty for any other error).
    public var ruleFieldErrors: [RuleFieldError] {
        guard case .problem(let p, _) = self, p.status == 422, p.code == "rule_pattern_invalid" || p.code == "invalid_rule" else { return [] }
        if let e = p.errors, !e.isEmpty { return e }
        if let f = p.field { return [RuleFieldError(field: f, message: p.detail ?? p.title, column: p.column)] }
        return []
    }
    /// True for failures worth retrying later (offline, 5xx, 429); false for permanent rejections.
    public var isRetryable: Bool {
        switch self {
        case .transport: true
        case .problem(let p, _): p.status >= 500 || p.status == 429
        case .http(let s): s >= 500 || s == 429
        default: false
        }
    }
}

/// Why a password sign-in did not succeed, in the terms the sign-in screen shows (D4 §1.5).
public enum LoginFailure: Error, Sendable, Equatable {
    /// 401 `invalid_credentials`.
    case invalidCredentials
    /// 429 with `Retry-After` (whole seconds, at least 1).
    case rateLimited(retryAfter: TimeInterval)
    /// Offline, DNS failure, timeout, connection refused.
    case unreachable
    /// TLS / certificate / ATS failure for `host`.
    case tls(host: String)
    /// 5xx.
    case server
    /// The address answers but not with the Inbox API (404, or a 2xx that isn't the login response).
    case notInboxServer
}
