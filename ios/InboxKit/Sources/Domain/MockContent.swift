import Foundation
import API
import Persistence

/// Canned message content (detail / body / raw / attachments) served by the offline preview API, so the reader
/// screens work in `-UseMockData` mode and in previews without a server.
public enum MockContent {
    public static let host = "preview.invalid"
    public static let invoiceID = MockData.messageID(1)
    public static let threadID = MockData.messageID(2)
    public static let kettleID = MockData.messageID(13)
    /// A message Gmail does not have (MS09): "Statement ready".
    public static let notFoundID = MockData.messageID(9)
    public static func attachmentID(_ n: Int) -> UUID { UUID(uuidString: String(format: "00000000-0000-4000-8000-0000000000B%01d", n))! }

    static let pngBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
    static let pdf = Data("%PDF-1.1\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 300 200]>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF".utf8)

    public static let invoiceHTML = """
    <html><body style="margin:0;font-family:-apple-system,Helvetica,Arial,sans-serif;color:#1d1d1f;background:#fff">
    <div style="padding:20px 16px">
    <img src="cid:logo@northgate.example" width="36" height="36" style="background:#1d1d1f;border-radius:6px" alt="">
    <div style="font-size:12px;letter-spacing:.08em;margin-top:10px;color:#666">NORTHGATE LTD</div>
    <h2 style="margin:6px 0 16px">Your October invoice</h2>
    <p>Hi Sam,</p>
    <p>Thanks for another month. Invoice #2041 for the October retainer is attached. Payment terms are 30 days.</p>
    <table style="width:100%;border-collapse:collapse;margin:16px 0">
    <tr><td style="padding:8px 0;border-bottom:1px solid #ddd">Retainer, October</td><td align="right" style="border-bottom:1px solid #ddd">$1,240.00</td></tr>
    <tr><td style="padding:8px 0"><b>Total due Oct 30</b></td><td align="right"><b>$1,240.00</b></td></tr>
    </table>
    <p><a href="https://northgate.example/invoices/2041" style="display:inline-block;background:#1d1d1f;color:#fff;padding:10px 16px;text-decoration:none;border-radius:6px">View invoice online</a></p>
    <p style="color:#888;font-size:12px">Northgate Ltd · 14 Mill Street · Leeds</p>
    </div></body></html>
    """

    public static let kettleHTML = """
    <html><body style="margin:0;font-family:-apple-system,Helvetica,Arial,sans-serif;color:#1d1d1f;background:#fff">
    <div style="padding:20px 16px">
    <img src="https://cdn.kettle.example/logo.png" width="120" height="32" alt="logo.png">
    <h2 style="margin:16px 0 8px">You have a new order</h2>
    <p>2 × Copper Kettle · $148.00 · paid by card ending 4242.</p>
    <img src="https://cdn.kettle.example/product.jpg" width="240" height="160" alt="product.jpg">
    <p><a href="https://dashboard.kettle.example/orders/1182">Open order #1182</a></p>
    </div></body></html>
    """

    // MARK: Routing

    /// Returns a response for preview-host URLs we can answer, nil for everything else (-> offline error).
    static func response(for url: URL) -> (status: Int, headers: [String: String], body: Data)? {
        guard url.host == host else { return nil }
        let parts = url.path.split(separator: "/").map(String.init)
        func ok(_ data: Data, _ type: String) -> (Int, [String: String], Data) { (200, ["Content-Type": type], data) }
        if parts.count >= 3, parts[0] == "v1", parts[1] == "messages", let id = UUID(uuidString: parts[2]), isMock(id) {
            switch parts.count {
            case 3: return jsonData(detail(id)).map { ok($0, "application/json") }
            case 4 where parts[3] == "body": return jsonData(body(id)).map { ok($0, "application/json") }
            case 4 where parts[3] == "raw": return ok(Data(raw(id).utf8), "message/rfc822")
            default: return nil
            }
        }
        if parts.count == 3, parts[0] == "v1", parts[1] == "attachments" {
            return (302, ["Location": "http://\(host)/files/\(parts[2])"], Data())
        }
        if parts.count == 2, parts[0] == "files" {
            let isPNG = parts[1].lowercased().hasSuffix("b2")
            return isPNG ? ok(Data(base64Encoded: pngBase64) ?? Data(), "image/png") : ok(pdf, "application/pdf")
        }
        return nil
    }

    static func isMock(_ id: UUID) -> Bool { id.uuidString.lowercased().hasPrefix("00000000-0000-4000-8000-") }

    private static func jsonData(_ obj: [String: Any]) -> Data? { try? JSONSerialization.data(withJSONObject: obj) }

    // MARK: Fixtures

    private static func iso(_ d: Date) -> String { ISO8601DateFormatter().string(from: d) }

    private static func messageObject(_ r: MessageRecord) -> [String: Any] {
        let e = APIClient.makeEncoder()
        e.dateEncodingStrategy = .iso8601
        guard let d = try? e.encode(r), let o = try? JSONSerialization.jsonObject(with: d) as? [String: Any] else { return [:] }
        return o
    }

    private static func record(_ id: UUID) -> MessageRecord? {
        if id == kettleID { return MockData.readerMessages().first { $0.id == id } }
        return MockData.messages().first { $0.id == id } ?? threadRecords().first { $0.id == id }
    }

    static func threadRecords(now: Date = AppClock.now) -> [MessageRecord] {
        let day: TimeInterval = 86400
        func m(_ n: Int, _ out: Bool, _ name: String, _ ago: TimeInterval, _ snippet: String) -> MessageRecord {
            MessageRecord(id: MockData.messageID(n), businessId: MockData.acme, direction: out ? "out" : "in",
                          fromAddr: out ? "sam@acme-studio.com" : "mara@example.com", fromName: name,
                          subject: "Re: Brand guidelines v3", snippet: snippet, toLocal: "hello",
                          receivedAt: now.addingTimeInterval(-ago), isRead: true, threadKey: "t2")
        }
        return [
            m(101, true, "Sam", 4 * day, "Here's v3 with the new palette and the type scale…"),
            m(102, false, "Mara Okafor", 3.8 * day, "Thanks! Sharing with the team today."),
            m(103, true, "Sam", 3 * day, "Any thoughts before Friday?"),
        ]
    }

    static func detail(_ id: UUID) -> [String: Any] {
        let rec = record(id) ?? MessageRecord(id: id, businessId: MockData.acme, receivedAt: AppClock.now)
        var thread: [[String: Any]] = [messageObject(rec)]
        if id == threadID { thread = (threadRecords() + [rec]).map(messageObject) }
        var attachments: [[String: Any]] = []
        if id == invoiceID {
            attachments = [
                ["id": attachmentID(1).uuidString.lowercased(), "filename": "Invoice-2041.pdf", "mime": "application/pdf", "size_bytes": 86_016, "content_id": NSNull(), "inline": false],
                ["id": attachmentID(2).uuidString.lowercased(), "filename": "logo.png", "mime": "image/png", "size_bytes": 2_048, "content_id": "logo@northgate.example", "inline": true],
            ]
        }
        let biz: [String: Any] = id == kettleID ? ["id": MockData.acme.uuidString.lowercased(), "name": "Kettle House", "domain": "kettle.house"]
            : ["id": MockData.acme.uuidString.lowercased(), "name": "Acme Studio", "domain": "acme-studio.com"]
        return [
            "message": messageObject(rec), "thread": thread, "attachments": attachments,
            "details": [
                "business": biz, "address": "\(rec.toLocal ?? "hello")@\(biz["domain"] as? String ?? "acme-studio.com")",
                "rule_name": "Invoices", "gmail_id": "18f2a1c0de",
                "gmail": id == notFoundID
                    ? ["state": "not_found", "last_error": "Gmail has no message with this ID.", "last_synced_at": NSNull()] as [String: Any]
                    : ["state": "synced", "last_error": NSNull(), "last_synced_at": iso(rec.receivedAt.addingTimeInterval(60))],
                "labels": ["Acme/Invoices"], "size_bytes": 98_304, "received_at": iso(rec.receivedAt),
            ] as [String: Any],
        ]
    }

    static func body(_ id: UUID) -> [String: Any] {
        let auth: [String: Any] = ["spf": "pass", "dkim": "pass (northgate.example)", "dmarc": "pass"]
        switch id {
        case invoiceID:
            return ["html": invoiceHTML, "text": NSNull(), "remote_image_count": 0, "auth_results": auth,
                    "inline": [["cid": "logo@northgate.example", "attachment_id": attachmentID(2).uuidString.lowercased(), "mime": "image/png"]]]
        case kettleID:
            return ["html": kettleHTML, "text": NSNull(), "remote_image_count": 2, "auth_results": auth, "inline": []]
        default:
            let text = (record(id)?.snippet ?? "Hello.") + "\n\nSee https://acme-studio.com/brand for the latest files.\n\n— Sent from the mock mailbox"
            return ["html": NSNull(), "text": text, "remote_image_count": 0, "auth_results": auth, "inline": []]
        }
    }

    static func raw(_ id: UUID) -> String {
        let r = record(id)
        return """
        Delivered-To: sam@gmail.com\r
        From: \(r?.fromName ?? "Sender") <\(r?.fromAddr ?? "sender@example.com")>\r
        To: \(r?.toLocal ?? "hello")@acme-studio.com\r
        Date: Sat, 11 Oct 2026 09:41:07 +0100\r
        Subject: \(r?.subject ?? "(no subject)")\r
        Message-ID: <8f2c1a@northgate.example>\r
        Authentication-Results: mx.example; dkim=pass; spf=pass; dmarc=pass\r
        X-Forwarded-To: sam@gmail.com\r
        Content-Type: multipart/mixed;\r
         boundary="b1"\r
        \r
        --b1\r
        Content-Type: text/html; charset=utf-8\r
        \r
        <html>…</html>\r
        --b1--\r
        """
    }
}
