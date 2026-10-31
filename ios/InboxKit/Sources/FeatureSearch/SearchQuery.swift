import Foundation
import API

/// Client-side reading of a query string: operators, quoted phrases and free words. Mirrors the server grammar
/// closely enough for mock-mode search and as the highlight fallback when the server sends no offsets.
public struct SearchQuery: Sendable, Equatable {
    public var words: [String] = []
    public var phrases: [String] = []
    public var from: [String] = []
    public var to: [String] = []
    public var hasAttachment = false

    public var terms: [String] { phrases + words }
    public var isEmpty: Bool { terms.isEmpty && from.isEmpty && to.isEmpty && !hasAttachment }

    public init(_ raw: String) {
        var tokens: [(text: String, quoted: Bool)] = []
        var cur = "", inQuote = false, hadQuote = false
        func flush() { if !cur.isEmpty { tokens.append((cur, hadQuote)) }; cur = ""; hadQuote = false }
        for c in raw {
            if c == "\"" { inQuote.toggle(); hadQuote = true }
            else if c.isWhitespace && !inQuote { flush() }
            else { cur.append(c) }
        }
        flush()
        for (token, quoted) in tokens {
            let lower = token.lowercased()
            if lower == "has:attachment" { hasAttachment = true }
            else if lower.hasPrefix("from:") { let v = String(token.dropFirst(5)); if !v.isEmpty { from.append(v) } }
            else if lower.hasPrefix("to:") { let v = String(token.dropFirst(3)); if !v.isEmpty { to.append(v) } }
            else if lower.hasPrefix("has:") { continue }
            else if quoted { phrases.append(token) }
            else { words.append(contentsOf: lower.split(whereSeparator: { !$0.isLetter && !$0.isNumber }).map(String.init)) }
        }
    }
}

/// Highlight helpers. Server spans are half-open **unicode scalar** offsets (the server counts runes).
public enum SearchHighlight {
    /// Case-insensitive occurrences of `terms` starting at a word boundary, merged and sorted.
    public static func spans(in text: String, terms: [String]) -> [Span] {
        let hay = fold(Array(text.unicodeScalars))
        var out: [Span] = []
        for term in terms where !term.isEmpty {
            let needle = fold(Array(term.unicodeScalars))
            guard needle.count <= hay.count else { continue }
            var i = 0
            while i + needle.count <= hay.count {
                let boundary = i == 0 || !isWordScalar(hay[i - 1])
                if boundary && Array(hay[i..<(i + needle.count)]) == needle { out.append(Span(start: i, end: i + needle.count)); i += needle.count } else { i += 1 }
            }
        }
        return merge(out)
    }

    public static func merge(_ spans: [Span]) -> [Span] {
        var out: [Span] = []
        for s in spans.sorted(by: { $0.start < $1.start }) where s.end > s.start {
            if let last = out.last, s.start <= last.end { out[out.count - 1].end = max(last.end, s.end) } else { out.append(s) }
        }
        return out
    }

    /// Pieces of `text` split at span edges; spans are clamped to the text length.
    public static func segments(_ text: String, spans: [Span]) -> [(text: String, highlighted: Bool)] {
        let scalars = Array(text.unicodeScalars)
        var out: [(String, Bool)] = []
        var pos = 0
        func piece(_ a: Int, _ b: Int) -> String { var v = String.UnicodeScalarView(); v.append(contentsOf: scalars[a..<b]); return String(v) }
        for s in merge(spans) {
            let a = min(max(s.start, pos), scalars.count), b = min(max(s.end, a), scalars.count)
            if a > pos { out.append((piece(pos, a), false)) }
            if b > a { out.append((piece(a, b), true)) }
            pos = max(pos, b)
        }
        if pos < scalars.count { out.append((piece(pos, scalars.count), false)) }
        return out
    }

    private static func isWordScalar(_ s: Unicode.Scalar) -> Bool { s.properties.isAlphabetic || (s.value >= 0x30 && s.value <= 0x39) }

    private static func fold(_ scalars: [Unicode.Scalar]) -> [Unicode.Scalar] {
        scalars.map { s in
            let l = String(s).lowercased().unicodeScalars
            return l.count == 1 ? l.first! : s
        }
    }
}
