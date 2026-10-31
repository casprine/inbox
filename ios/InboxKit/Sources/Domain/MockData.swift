import Foundation
import API
import Persistence

/// Deterministic sample data for previews, tests and the `-UseMockData` launch mode.
public enum MockData {
    public static let acme = UUID(uuidString: "00000000-0000-4000-8000-0000000000A1")!
    public static let northgate = UUID(uuidString: "00000000-0000-4000-8000-0000000000A2")!
    public static let lumen = UUID(uuidString: "00000000-0000-4000-8000-0000000000A3")!

    public static func messageID(_ n: Int) -> UUID {
        UUID(uuidString: String(format: "00000000-0000-4000-8000-%012d", n))!
    }

    public static func businesses(now: Date = .now) -> [BusinessRecord] {
        func rec(_ id: UUID, _ name: String, _ domain: String, _ order: Int, _ unread: Int, _ addrs: [(String, Int)]) -> BusinessRecord {
            let json = "[" + addrs.map { #"{"local_part":"\#($0.0)","count":\#($0.1),"last_at":null}"# }.joined(separator: ",") + "]"
            return BusinessRecord(id: id, name: name, domain: domain, sortOrder: order, hidden: false, enabled: true,
                                  routing: id == lumen ? "to_email" : "ok", unread: unread,
                                  lastMessageAt: id == lumen ? now.addingTimeInterval(-6 * 86_400) : now, addressesJSON: json)
        }
        return [
            rec(acme, "Acme Studio", "acme-studio.com", 0, 2, [("invoices", 2), ("hello", 3), ("press", 2)]),
            rec(northgate, "Northgate Ltd", "northgate.co", 1, 1, [("accounts", 2), ("hello", 1)]),
            rec(lumen, "Lumen Hosting", "lumen.host", 2, 0, [("billing", 2)]),
        ]
    }

    public static func messages(now: Date = .now) -> [MessageRecord] {
        let h: TimeInterval = 3600
        func m(_ n: Int, _ biz: UUID, _ to: String, _ from: String, _ subject: String, _ snippet: String, ago: TimeInterval, read: Bool) -> MessageRecord {
            MessageRecord(id: messageID(n), businessId: biz, fromAddr: "\(from.lowercased().replacingOccurrences(of: " ", with: "."))@example.com",
                          fromName: from, subject: subject, snippet: snippet, toLocal: to, receivedAt: now.addingTimeInterval(-ago),
                          isRead: read, threadKey: "t\(n)")
        }
        return [
            m(1, acme, "invoices", "Northgate Ltd", "Invoice #2041 — October retainer", "Hi Sam, thanks for another month. Invoice #2041 for the October retainer is attached.", ago: 0.5 * h, read: false),
            m(2, acme, "hello", "Mara Okafor", "Re: Brand guidelines v3", "Love the palette. Could we try a darker version of the logo before Friday's review?", ago: 1.5 * h, read: false),
            m(3, acme, "invoices", "Lumen Hosting", "Invoice LH-5521", "Your invoice for November hosting is ready. Amount due on Nov 4.", ago: 50 * h, read: true),
            m(4, acme, "hello", "Dana Whitfield", "Contract renewal for 2027", "Attached is the renewal. Let me know if the terms work and I'll send it for signature.", ago: 28 * h, read: true),
            m(5, acme, "hello", "Studio Collective", "Workshop invite, Nov 14", "We're hosting a small workshop on type for screens and would love you to join.", ago: 80 * h, read: true),
            m(6, acme, "press", "Kerning Weekly", "Feature request: studio profile", "We'd love to include Acme in our studio profiles series next month.", ago: 100 * h, read: true),
            m(7, acme, "press", "Hannah Cole", "Interview follow-up", "Thanks again for the time on Tuesday. Here are the quotes for review.", ago: 130 * h, read: true),
            m(8, northgate, "accounts", "Priya Raman", "Q3 reconciliation", "Numbers match except for the October credit note; can you confirm?", ago: 3 * h, read: false),
            m(9, northgate, "accounts", "Bank Alerts", "Statement ready", "Your September statement is now available to download.", ago: 70 * h, read: true),
            m(10, northgate, "hello", "Tomás Weber", "Intro from Dana", "Dana mentioned you might be taking on new projects this winter.", ago: 200 * h, read: true),
            m(11, lumen, "billing", "Lumen Hosting", "Payment received", "Thanks, we received your payment of $48.00.", ago: 240 * h, read: true),
            m(12, lumen, "billing", "Lumen Hosting", "Invoice LH-5398", "Your invoice for October hosting is ready.", ago: 400 * h, read: true),
        ]
    }

    /// Extra rows for the reader screens (blocked-images email). Seeded only with `includeReader`.
    public static func readerMessages(now: Date = .now) -> [MessageRecord] {
        [MessageRecord(id: messageID(13), businessId: acme, fromAddr: "orders@stripe.example", fromName: "Stripe",
                       subject: "New order #1182", snippet: "You have a new order. 2 × Copper Kettle · $148.00.", toLocal: "orders",
                       receivedAt: now.addingTimeInterval(-0.2 * 3600), isRead: false, threadKey: "t13")]
    }

    /// Fills the cache; attention counts live in prefs (`attention.unrouted`, `attention.failed`).
    public static func seed(_ db: AppDatabase, now: Date = .now, includeReader: Bool = false) async throws {
        try await db.replaceBusinesses(businesses(now: now))
        try await db.upsertMessages(messages(now: now) + (includeReader ? readerMessages(now: now) : []))
        try await db.setPref("attention.unrouted", "3")
        try await db.setPref("attention.failed", "1")
        try await db.replaceRules(MockRules.records())
    }
}

extension AppEnvironment {
    /// Offline environment pre-filled with `MockData` (no server needed).
    public static func mock() async throws -> AppEnvironment {
        let env = try preview()
        try await MockData.seed(env.database)
        return env
    }
}
