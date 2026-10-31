import Foundation

/// JSON value for event fields the client does not model yet.
public enum JSONValue: Codable, Sendable, Hashable {
    case null, bool(Bool), number(Double), string(String), array([JSONValue]), object([String: JSONValue])
    public init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if c.decodeNil() { self = .null }
        else if let b = try? c.decode(Bool.self) { self = .bool(b) }
        else if let n = try? c.decode(Double.self) { self = .number(n) }
        else if let s = try? c.decode(String.self) { self = .string(s) }
        else if let a = try? c.decode([JSONValue].self) { self = .array(a) }
        else { self = .object(try c.decode([String: JSONValue].self)) }
    }
    public func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .null: try c.encodeNil()
        case .bool(let v): try c.encode(v)
        case .number(let v): try c.encode(v)
        case .string(let v): try c.encode(v)
        case .array(let v): try c.encode(v)
        case .object(let v): try c.encode(v)
        }
    }
    public var stringValue: String? { if case .string(let s) = self { s } else { nil } }
}

/// Server event envelope: `{"type": "...", ...fields}` (server/internal/events).
public enum ServerEvent: Sendable, Equatable {
    case mailNew(MailNew)
    case gmailStatus([String: JSONValue])
    case jobProgress([String: JSONValue])
    case healthChanged([String: JSONValue])
    case unknown(type: String, fields: [String: JSONValue])

    public struct MailNew: Sendable, Equatable {
        public var messageId: String
        public var businessId: UUID?
        public var route: String?
        public var state: String?
        public var receivedAt: Date?
    }

    /// Decodes one SSE `data:` payload; returns nil if it is not an envelope with a `type`.
    public static func decode(_ data: Data) -> ServerEvent? {
        guard var fields = try? JSONDecoder().decode([String: JSONValue].self, from: data),
              case .string(let type)? = fields.removeValue(forKey: "type") else { return nil }
        switch type {
        case "mail.new":
            guard let id = fields["message_id"]?.stringValue else { return nil }
            return .mailNew(.init(messageId: id,
                                  businessId: fields["business_id"]?.stringValue.flatMap(UUID.init(uuidString:)),
                                  route: fields["route"]?.stringValue, state: fields["state"]?.stringValue,
                                  receivedAt: fields["received_at"]?.stringValue.flatMap(APIClient.parseDate)))
        case "gmail.status": return .gmailStatus(fields)
        case "job.progress": return .jobProgress(fields)
        case "health.changed": return .healthChanged(fields)
        default: return .unknown(type: type, fields: fields)
        }
    }
}

/// Incremental SSE line parser (WHATWG event-stream rules, enough for this server).
public struct SSEParser: Sendable {
    public struct Frame: Sendable, Equatable { public var id: String?; public var event: String?; public var data: String }
    private var id: String?, event: String?, data: [String] = []
    /// Reconnection delay announced by the server's `retry:` field.
    public private(set) var retry: TimeInterval?
    public init() {}

    /// Feed one line without its terminator. An empty line dispatches a frame (if any data).
    public mutating func feed(line: String) -> Frame? {
        if line.isEmpty {
            defer { id = nil; event = nil; data = [] }
            return data.isEmpty ? nil : Frame(id: id, event: event, data: data.joined(separator: "\n"))
        }
        if line.hasPrefix(":") { return nil }
        let name: Substring, value: Substring
        if let i = line.firstIndex(of: ":") {
            name = line[..<i]
            var v = line[line.index(after: i)...]
            if v.hasPrefix(" ") { v = v.dropFirst() }
            value = v
        } else { name = Substring(line); value = "" }
        switch name {
        case "data": data.append(String(value))
        case "event": event = String(value)
        case "id": id = String(value)
        case "retry": if let ms = Double(value) { retry = ms / 1000 }
        default: break
        }
        return nil
    }
}

/// Foreground SSE consumer with exponential-backoff reconnect.
public final class SSEClient: Sendable {
    private let api: APIClient
    private let minBackoff: TimeInterval, maxBackoff: TimeInterval

    public init(api: APIClient, minBackoff: TimeInterval = 1, maxBackoff: TimeInterval = 30) {
        self.api = api; self.minBackoff = minBackoff; self.maxBackoff = maxBackoff
    }

    /// Never finishes on its own: reconnects until the consumer cancels. After a (re)connect it yields
    /// `.reconnected` so callers can refetch (the server has no Last-Event-ID replay).
    public func events() -> AsyncStream<Update> {
        AsyncStream { continuation in
            let task = Task { [api, minBackoff, maxBackoff] in
                var backoff = minBackoff
                var first = true
                while !Task.isCancelled {
                    var parser = SSEParser()
                    do {
                        let req = try await api.eventStreamRequest()
                        let (bytes, resp) = try await api.streamSession.bytes(for: req)
                        guard let http = resp as? HTTPURLResponse else { throw APIError.transport("non-HTTP") }
                        guard http.statusCode == 200 else {
                            var body = Data()
                            for try await b in bytes { body.append(b); if body.count > 8192 { break } }
                            let err = APIClient.decodeError(body, http)
                            if err.status == 401 { continuation.yield(.unauthorized); continuation.finish(); return }
                            throw err
                        }
                        backoff = minBackoff
                        if !first { continuation.yield(.reconnected) }
                        first = false
                        var line = Data()
                        for try await byte in bytes {
                            if byte == 0x0A {
                                if line.last == 0x0D { line.removeLast() }
                                if let f = parser.feed(line: String(decoding: line, as: UTF8.self)),
                                   let ev = ServerEvent.decode(Data(f.data.utf8)) { continuation.yield(.event(ev)) }
                                line.removeAll(keepingCapacity: true)
                            } else { line.append(byte) }
                        }
                    } catch is CancellationError { break }
                    catch { if Task.isCancelled { break } }
                    let delay = min(max(backoff, parser.retry ?? 0), maxBackoff)
                    try? await Task.sleep(for: .seconds(delay))
                    backoff = min(backoff * 2, maxBackoff)
                }
                continuation.finish()
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    public enum Update: Sendable, Equatable {
        case event(ServerEvent)
        case reconnected
        case unauthorized
    }
}
