#if os(iOS)
import WebKit

/// `WKContentRuleList` JSON. Everything is blocked except `data:` / `about:` and the `inbox-cid` scheme; the
/// "images allowed" variant additionally lets http(s) image loads through (load-images-for-sender).
public enum ContentRules {
    public static func json(allowImages: Bool) -> String {
        var rules = [
            #"{"trigger":{"url-filter":".*"},"action":{"type":"block"}}"#,
            #"{"trigger":{"url-filter":"^data:"},"action":{"type":"ignore-previous-rules"}}"#,
            #"{"trigger":{"url-filter":"^about:"},"action":{"type":"ignore-previous-rules"}}"#,
            #"{"trigger":{"url-filter":"^\#(HTMLDocument.cidScheme):"},"action":{"type":"ignore-previous-rules"}}"#,
        ]
        if allowImages {
            rules.append(#"{"trigger":{"url-filter":"^https?:","resource-type":["image"]},"action":{"type":"ignore-previous-rules"}}"#)
        }
        return "[" + rules.joined(separator: ",") + "]"
    }

    @MainActor
    public static func compile(allowImages: Bool) async throws -> WKContentRuleList {
        let id = allowImages ? "inbox.rules.images.v1" : "inbox.rules.blockall.v1"
        guard let list = try await WKContentRuleListStore.default().compileContentRuleList(forIdentifier: id, encodedContentRuleList: json(allowImages: allowImages)) else {
            throw URLError(.cannotParseResponse)
        }
        return list
    }
}
#endif
