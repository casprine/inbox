import Foundation
import API
import Domain
import Persistence

/// Senders whose remote images the user chose to load. Local pref for now; server prefs come with T27.
public struct ImageAllowList: Sendable {
    public static let prefKey = "image_allow_senders"
    let db: AppDatabase
    public init(_ db: AppDatabase) { self.db = db }

    public func senders() async -> Set<String> {
        guard let raw = try? await db.pref(Self.prefKey), let d = raw.data(using: .utf8),
              let arr = try? JSONDecoder().decode([String].self, from: d) else { return [] }
        return Set(arr)
    }

    public func allow(_ sender: String) async {
        var all = await senders()
        all.insert(Self.normalize(sender))
        if let d = try? JSONEncoder().encode(all.sorted()), let s = String(data: d, encoding: .utf8) { try? await db.setPref(Self.prefKey, s) }
    }

    public static func normalize(_ sender: String) -> String { sender.trimmingCharacters(in: .whitespaces).lowercased() }
}

public enum GmailLinks {
    public static let app = URL(string: "googlegmail://")!
    /// Web fallback; deep-links to the message when the Gmail id is known.
    public static func web(gmailID: String?) -> URL {
        URL(string: "https://mail.google.com/mail/u/0/#all" + (gmailID.map { "/\($0)" } ?? ""))!
    }
}

/// Splits an RFC 5322 message into unfolded headers and the remaining source.
public struct RawMessage: Sendable, Equatable {
    public var headers: [(name: String, value: String)]
    public var source: String
    public static func == (a: RawMessage, b: RawMessage) -> Bool {
        a.source == b.source && a.headers.elementsEqual(b.headers) { $0 == $1 }
    }

    public static func parse(_ data: Data, sourceLimit: Int = 20_000) -> RawMessage {
        let text = String(data: data, encoding: .utf8) ?? String(decoding: data, as: UTF8.self)
        let normalized = text.replacingOccurrences(of: "\r\n", with: "\n")
        let split = normalized.range(of: "\n\n")
        let head = split.map { String(normalized[..<$0.lowerBound]) } ?? normalized
        var rest = split.map { String(normalized[$0.upperBound...]) } ?? ""
        if rest.count > sourceLimit { rest = String(rest.prefix(sourceLimit)) + "\n… (truncated)" }
        var headers: [(String, String)] = []
        for line in head.split(separator: "\n", omittingEmptySubsequences: true) {
            if (line.first == " " || line.first == "\t"), !headers.isEmpty {
                headers[headers.count - 1].1 += " " + line.trimmingCharacters(in: .whitespaces)
            } else if let c = line.firstIndex(of: ":") {
                headers.append((String(line[..<c]), line[line.index(after: c)...].trimmingCharacters(in: .whitespaces)))
            }
        }
        return RawMessage(headers: headers, source: rest)
    }
}

enum MessageFormat {
    static func clock(_ date: Date, now: Date = AppClock.now, calendar: Calendar = .current) -> String {
        let f = DateFormatter(); f.calendar = calendar; f.timeZone = calendar.timeZone; f.locale = .current
        f.setLocalizedDateFormatFromTemplate(calendar.isDate(date, inSameDayAs: now) ? "jmm" : "MMMd jmm")
        return f.string(from: date)
    }
    static func day(_ date: Date, now: Date = AppClock.now, calendar: Calendar = .current) -> String {
        let f = DateFormatter(); f.calendar = calendar; f.timeZone = calendar.timeZone; f.locale = .current
        if calendar.isDate(date, inSameDayAs: now) { f.setLocalizedDateFormatFromTemplate("jmm") }
        else if let d = calendar.dateComponents([.day], from: calendar.startOfDay(for: date), to: calendar.startOfDay(for: now)).day, d < 7 { f.setLocalizedDateFormatFromTemplate("EEE") }
        else { f.setLocalizedDateFormatFromTemplate("MMMd") }
        return f.string(from: date)
    }
    static func size(_ bytes: Int?) -> String? {
        bytes.map { ByteCountFormatter.string(fromByteCount: Int64($0), countStyle: .file) }
    }
    static func senderName(_ m: Message) -> String {
        let base = [m.fromName, m.fromAddr].compactMap { $0 }.first { !$0.isEmpty } ?? "Unknown sender"
        return m.direction == "out" ? "\(m.fromName?.isEmpty == false ? m.fromName! : "You") (you)" : base
    }
    /// Up to two initials ("NL" for Northgate Ltd).
    static func initials(_ m: Message) -> String {
        let base = [m.fromName, m.fromAddr].compactMap { $0 }.first { !$0.isEmpty } ?? "?"
        let words = base.split { !$0.isLetter && !$0.isNumber }.prefix(2)
        return words.compactMap { $0.first.map { String($0).uppercased() } }.joined()
    }
    static func initial(_ m: Message) -> String {
        String(([m.fromName, m.fromAddr].compactMap { $0 }.first { !$0.isEmpty } ?? "?").prefix(1)).uppercased()
    }
    static func gmailStatus(_ s: String?) -> String {
        switch s {
        case "synced": "In sync"
        case "pending": "Waiting"
        case "not_found": "Not found"
        case "error": "Error"
        case "signed_out": "Signed out"
        case .some(let x): x.replacingOccurrences(of: "_", with: " ").capitalized
        case nil: "Not connected"
        }
    }
}
