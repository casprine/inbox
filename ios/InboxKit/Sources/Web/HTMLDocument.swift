import Foundation

/// Pure string preparation for message HTML. The body is shown "as sent": we only add a viewport, force a
/// light colour scheme with a white page, and point `cid:` references at the `inbox-cid` scheme.
public enum HTMLDocument {
    public static let cidScheme = "inbox-cid"

    static let headInjection = """
    <meta name="viewport" content="width=device-width, initial-scale=1"><meta name="color-scheme" content="light">\
    <style>:root{color-scheme:light}html{background:#fff}body{background:#fff;margin:0;padding:0;\
    overflow-wrap:anywhere;-webkit-text-size-adjust:100%}img{max-width:100%;height:auto}\
    .inbox-blocked{box-sizing:border-box;display:flex;align-items:center;justify-content:center;margin:4px auto;max-width:100%;\
    border:1px dashed #bbb;color:#888;font:11px -apple-system,sans-serif;text-align:center;overflow:hidden}</style>
    """

    public static func prepare(_ html: String, allowImages: Bool = true) -> String {
        var body = rewriteCIDs(html)
        if !allowImages { body = blockRemoteImages(body) }
        if let r = body.range(of: "<head[^>]*>", options: [.regularExpression, .caseInsensitive]) {
            return body.replacingCharacters(in: r, with: String(body[r]) + headInjection)
        }
        if let r = body.range(of: "<html[^>]*>", options: [.regularExpression, .caseInsensitive]) {
            return body.replacingCharacters(in: r, with: String(body[r]) + "<head>" + headInjection + "</head>")
        }
        return "<!doctype html><html><head>" + headInjection + "</head><body>" + body + "</body></html>"
    }

    /// Swaps remote `<img>` tags for a dashed "name · blocked" box (the content rules already stop the load; this is what the reader sees).
    /// `cid:` and `data:` images stay, so inline logos from the message itself still render.
    public static func blockRemoteImages(_ html: String) -> String {
        guard let re = try? NSRegularExpression(pattern: #"<img\b[^>]*>"#, options: .caseInsensitive) else { return html }
        let ns = html as NSString
        var out = "", last = 0
        for m in re.matches(in: html, range: NSRange(location: 0, length: ns.length)) {
            let tag = ns.substring(with: m.range)
            out += ns.substring(with: NSRange(location: last, length: m.range.location - last))
            last = m.range.location + m.range.length
            let src = attribute("src", in: tag) ?? ""
            let lower = src.lowercased()
            if lower.hasPrefix("data:") || lower.hasPrefix("\(cidScheme):") { out += tag; continue }
            let name = src.split(separator: "?").first.map { String($0.split(separator: "/").last ?? "") } ?? ""
            let label = name.isEmpty ? "image" : name
            let h = attribute("height", in: tag).flatMap { Int($0.filter(\.isNumber)) }.map { min(max($0, 24), 400) } ?? (label.count > 0 ? 44 : 120)
            let escaped = label.replacingOccurrences(of: "&", with: "&amp;").replacingOccurrences(of: "<", with: "&lt;").replacingOccurrences(of: ">", with: "&gt;")
            out += "<div class=\"inbox-blocked\" style=\"height:\(h)px\">\(escaped) · blocked</div>"
        }
        return out + ns.substring(from: last)
    }

    private static func attribute(_ name: String, in tag: String) -> String? {
        guard let re = try? NSRegularExpression(pattern: "\\b\(name)\\s*=\\s*(?:\"([^\"]*)\"|'([^']*)'|([^\\s>]+))", options: .caseInsensitive),
              let m = re.firstMatch(in: tag, range: NSRange(location: 0, length: (tag as NSString).length)) else { return nil }
        for i in 1...3 where m.range(at: i).location != NSNotFound { return (tag as NSString).substring(with: m.range(at: i)) }
        return nil
    }

    /// `src="cid:abc@x"` / `url(cid:abc)` -> `inbox-cid://m/abc@x`.
    public static func rewriteCIDs(_ html: String) -> String {
        guard html.range(of: "cid:", options: .caseInsensitive) != nil,
              let re = try? NSRegularExpression(pattern: #"(["'(=\s])cid:([^"'\s)>]+)"#, options: .caseInsensitive) else { return html }
        let ns = html as NSString
        return re.stringByReplacingMatches(in: html, range: NSRange(location: 0, length: ns.length),
                                           withTemplate: "$1\(cidScheme)://m/$2")
    }

    /// Normalises a Content-ID for lookups: no angle brackets, no `cid:` prefix, lowercase, percent-decoded.
    public static func normalizeCID(_ raw: String) -> String {
        var s = raw.removingPercentEncoding ?? raw
        s = s.trimmingCharacters(in: CharacterSet(charactersIn: "<> \t\n"))
        if s.lowercased().hasPrefix("cid:") { s.removeFirst(4) }
        return s.lowercased()
    }
}
