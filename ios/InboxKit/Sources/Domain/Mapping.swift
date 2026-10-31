import Foundation
import API
import Persistence

extension MessageRecord {
    public init(_ m: API.Message) {
        self.init(id: m.id, businessId: m.businessId, direction: m.direction, fromAddr: m.fromAddr, fromName: m.fromName,
                  subject: m.subject, snippet: m.snippet, toLocal: m.toLocal, toTag: m.toTag, receivedAt: m.receivedAt,
                  isRead: m.isRead, archived: m.archived, attachmentCount: m.attachmentCount, threadKey: m.threadKey, route: m.route)
    }
}

extension BusinessRecord {
    public init(_ b: API.Business) {
        let enc = APIClient.makeEncoder()
        let json = (try? enc.encode(b.addresses)).flatMap { String(data: $0, encoding: .utf8) } ?? "[]"
        self.init(id: b.id, name: b.name, domain: b.domain, sortOrder: b.sortOrder, hidden: b.hidden, enabled: b.enabled,
                  routing: b.routing, unread: b.unread, lastMessageAt: b.lastMessageAt, addressesJSON: json)
    }
    public var addresses: [API.Address] {
        (try? APIClient.makeDecoder().decode([API.Address].self, from: Data(addressesJSON.utf8))) ?? []
    }
}

extension MessageFilter {
    var query: MessageListQuery { MessageListQuery(business: businessId, unread: unreadOnly ? true : nil) }
}
