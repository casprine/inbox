import Foundation

/// Link detection for plain-text bodies (laid out natively, never in a web view). Only http(s) links are made tappable.
public enum PlainText {
    public static func attributed(_ text: String) -> AttributedString {
        var out = AttributedString(text)
        guard let det = try? NSDataDetector(types: NSTextCheckingResult.CheckingType.link.rawValue) else { return out }
        let ns = text as NSString
        for m in det.matches(in: text, range: NSRange(location: 0, length: ns.length)) {
            guard let url = m.url, let scheme = url.scheme?.lowercased(), scheme == "http" || scheme == "https",
                  let sr = Range(m.range, in: text), let ar = Range(sr, in: out) else { continue }
            out[ar].link = url
            out[ar].underlineStyle = .single
        }
        return out
    }
}
