import Foundation
import Observation
import API
import Domain
import Persistence
import Web

public enum BodyState: Sendable, Equatable {
    case loading, ready(MessageBody), failed
}

/// The open message / thread: detail, per-message bodies, image allow-list, actions.
@MainActor @Observable
public final class MessageModel {
    public enum Phase: Equatable, Sendable { case loading, loaded, failed(String) }

    public let id: UUID
    public private(set) var phase: Phase = .loading
    public private(set) var detail: MessageDetail?
    public private(set) var bodies: [UUID: BodyState] = [:]
    public var expanded: Set<UUID> = []
    public private(set) var allowedSenders: Set<String> = []
    public var notice: String?
    public private(set) var markedUnread = false
    public private(set) var isRetrying = false
    /// True while the screen shows saved copies because the server couldn't be reached.
    public private(set) var isOffline = false

    private let env: AppEnvironment
    public init(env: AppEnvironment, id: UUID) { self.env = env; self.id = id }

    /// Thread oldest first, always containing the opened message.
    public var thread: [Message] {
        guard let d = detail else { return [] }
        var t = d.thread.isEmpty ? [d.message] : d.thread
        if !t.contains(where: { $0.id == d.message.id }) { t.append(d.message) }
        return t.sorted { $0.receivedAt < $1.receivedAt }
    }
    public var latest: Message? { thread.last }
    public var domain: String? { detail?.details.business?.domain }

    public func toAddress(_ m: Message) -> String? {
        if m.id == detail?.message.id, let a = detail?.details.address, a.contains("@") { return a }
        return m.toLocal.map { l in domain.map { "\(l)@\($0)" } ?? l }
    }

    public var attachments: [Attachment] { (detail?.attachments ?? []).filter { !($0.inline && $0.contentId != nil) } }
    public var gmailState: String? { detail?.details.gmail?.state }

    public func load() async {
        allowedSenders = await ImageAllowList(env.database).senders()
        do {
            let d: MessageDetail
            do { d = try await env.api.message(id: id); await cache(d, key: "detail:\(id.uuidString)") }
            catch {
                // Offline (or the server is down): fall back to the saved copy when there is one.
                guard Self.isConnectivity(error), let saved: MessageDetail = await cached("detail:\(id.uuidString)") else { throw error }
                isOffline = true
                d = saved
            }
            detail = d
            phase = .loaded
            if let last = latest { expanded.insert(last.id) }
            expanded.insert(id)
            await loadBody(id)
            if let last = latest, last.id != id { await loadBody(last.id) }
            if !d.message.isRead && !isOffline { try? await env.mail.setRead(true, ids: [id]) }
        } catch {
            phase = .failed(Self.describe(error))
        }
    }

    public func loadBody(_ mid: UUID) async {
        if case .ready = bodies[mid] { return }
        bodies[mid] = .loading
        do {
            let b = try await env.api.messageBody(id: mid)
            bodies[mid] = .ready(b)
            await cache(b, key: "body:\(mid.uuidString)")
        } catch {
            if Self.isConnectivity(error), let saved: MessageBody = await cached("body:\(mid.uuidString)") { bodies[mid] = .ready(saved); isOffline = true }
            else { bodies[mid] = .failed }
        }
    }

    // MARK: Offline cache

    private func cache<T: Encodable>(_ value: T, key: String) async {
        guard let data = try? APIClient.makeCacheEncoder().encode(value), let json = String(data: data, encoding: .utf8) else { return }
        try? await env.database.cachePut(key, json: json)
    }
    private func cached<T: Decodable>(_ key: String) async -> T? {
        guard let json = (try? await env.database.cacheGet(key)) ?? nil else { return nil }
        return try? APIClient.makeDecoder().decode(T.self, from: Data(json.utf8))
    }
    /// Only "couldn't reach the server" falls back to the cache; a 404 (deleted message) or 401 must show the real error.
    nonisolated static func isConnectivity(_ error: Error) -> Bool {
        guard let e = error as? APIError else { return (error as? URLError) != nil }
        switch e {
        case .transport: return true
        case .problem(let p, _): return p.status >= 500
        case .http(let s): return s >= 500
        default: return false
        }
    }

    public func toggle(_ mid: UUID) async {
        if expanded.contains(mid) { expanded.remove(mid) } else { expanded.insert(mid); await loadBody(mid) }
    }

    // MARK: Images

    public func senderKey(_ m: Message) -> String { ImageAllowList.normalize(m.fromAddr ?? "") }
    public func imagesAllowed(for m: Message) -> Bool { allowedSenders.contains(senderKey(m)) }
    public func imagesBlocked(for m: Message, body: MessageBody) -> Bool { body.remoteImageCount > 0 && !imagesAllowed(for: m) }

    public func loadImages(for m: Message) async {
        let key = senderKey(m)
        guard !key.isEmpty else { return }
        await ImageAllowList(env.database).allow(key)
        allowedSenders.insert(key)
        await PrefsSync.pushAllowList(env)
    }

    /// Resolves `cid:` parts through the API (the web view can't carry the bearer header itself).
    public func cidResolver(for body: MessageBody) -> @Sendable (String) async -> CIDResource? {
        let api = env.api
        let map = Dictionary(body.inline.compactMap { i in i.attachmentId.map { (HTMLDocument.normalizeCID(i.cid), ($0, i.mime)) } },
                             uniquingKeysWith: { a, _ in a })
        return { cid in
            guard let (aid, mime) = map[cid], let data = try? await api.attachmentData(id: aid) else { return nil }
            return CIDResource(data: data, mime: mime)
        }
    }

    // MARK: Actions

    public func markUnread() async {
        try? await env.mail.setRead(false, ids: [id])
        markedUnread = true
        notice = "Marked as unread"
    }

    public func archive() async { try? await env.mail.setArchived(true, ids: [id]) }

    public func downloadAttachment(_ a: Attachment) async -> URL? {
        do {
            let data = try await env.api.attachmentData(id: a.id)
            let dir = FileManager.default.temporaryDirectory.appendingPathComponent("attachments/\(a.id.uuidString)", isDirectory: true)
            try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
            let name = (a.filename?.isEmpty == false ? a.filename! : "attachment").replacingOccurrences(of: "/", with: "_")
            let url = dir.appendingPathComponent(name)
            try data.write(to: url, options: .atomic)
            return url
        } catch {
            notice = "Couldn't download the attachment"
            return nil
        }
    }

    public func retryGmail() async {
        isRetrying = true; defer { isRetrying = false }
        do {
            try await env.api.gmailRetry(id: id)
            notice = "Checking Gmail again"
            if let d = try? await env.api.message(id: id) { detail = d }
        } catch let e as APIError where e.status == 404 || e.status == 501 {
            notice = "Retry isn't available on this server yet"
        } catch {
            notice = "Couldn't reach the server. Try again later."
        }
    }

    static func describe(_ e: Error) -> String {
        if let a = e as? APIError, a.status == 404 { return "This message is no longer stored." }
        return "Couldn't load this message. Check your connection and try again."
    }
}

/// Details / Original screens share one loader.
@MainActor @Observable
public final class MessageDetailsModel {
    public private(set) var detail: MessageDetail?
    public private(set) var error: String?
    public private(set) var raw: RawMessage?
    public private(set) var auth: MessageBody.AuthResults?
    public var notice: String?
    public private(set) var isRetrying = false
    private let env: AppEnvironment
    public let id: UUID
    public init(env: AppEnvironment, id: UUID) { self.env = env; self.id = id }

    public func loadDetails() async {
        do { detail = try await env.api.message(id: id); error = nil }
        catch { self.error = MessageModel.describe(error) }
    }

    public func loadOriginal() async {
        do {
            async let data = env.api.messageRaw(id: id)
            async let body = env.api.messageBody(id: id)
            raw = RawMessage.parse(try await data)
            auth = try? await body.authResults
            error = nil
        } catch { self.error = MessageModel.describe(error) }
    }

    public func retry() async {
        isRetrying = true; defer { isRetrying = false }
        do {
            try await env.api.gmailRetry(id: id)
            notice = "Checking Gmail again"
            await loadDetails()
        } catch let e as APIError where e.status == 404 || e.status == 501 {
            notice = "Retry isn't available on this server yet"
        } catch {
            notice = "Couldn't reach the server. Try again later."
        }
    }
}
