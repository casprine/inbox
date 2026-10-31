import Foundation

/// Turns what the person typed in "Server address" into the base URL the API client uses (D4 §1.3).
/// `https://` is added when there is no scheme; host is lower-cased; path, query and trailing slash are dropped; an explicit port is kept.
/// Plain `http://` is only accepted for hosts on this device or local network (localhost, `*.local`, `*.localhost`, loopback and private IPv4).
public enum ServerAddress {
    public enum Invalid: Error, Equatable, Sendable {
        /// Empty, spaces inside, no dot (and not a local name), or an unsupported scheme.
        case malformed
        /// `http://` to a host that is not local.
        case insecure
    }

    public static func normalise(_ input: String) -> Result<URL, Invalid> {
        let typed = input.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !typed.isEmpty, typed.rangeOfCharacter(from: .whitespacesAndNewlines) == nil else { return .failure(.malformed) }
        var text = typed
        if let r = text.range(of: "://") {
            let scheme = text[..<r.lowerBound].lowercased()
            guard scheme == "http" || scheme == "https" else { return .failure(.malformed) }
        } else {
            guard !text.contains(":") || text.firstIndex(of: ":").map({ text[text.index(after: $0)...].first?.isNumber ?? false }) == true else { return .failure(.malformed) }
            text = "https://" + text
        }
        guard let c = URLComponents(string: text), let scheme = c.scheme?.lowercased(), let rawHost = c.host, !rawHost.isEmpty else { return .failure(.malformed) }
        let host = rawHost.lowercased()
        guard isPlausible(host) else { return .failure(.malformed) }
        if scheme == "http", !isLocal(host) { return .failure(.insecure) }
        var out = URLComponents()
        out.scheme = scheme
        out.host = host
        out.port = c.port
        guard let url = out.url else { return .failure(.malformed) }
        return .success(url)
    }

    static func isPlausible(_ host: String) -> Bool {
        guard host.allSatisfy({ $0.isLetter || $0.isNumber || $0 == "." || $0 == "-" || $0 == "_" }) else { return false }
        if host.hasPrefix(".") || host.hasSuffix(".") || host.contains("..") { return false }
        return host == "localhost" || host.contains(".")
    }

    static func isLocal(_ host: String) -> Bool {
        if host == "localhost" || host.hasSuffix(".localhost") || host.hasSuffix(".local") { return true }
        let parts = host.split(separator: ".", omittingEmptySubsequences: false).compactMap { Int($0) }
        guard parts.count == 4, host.split(separator: ".").count == 4, parts.allSatisfy({ (0...255).contains($0) }) else { return false }
        switch (parts[0], parts[1]) {
        case (10, _), (127, _), (192, 168): return true
        case (172, 16...31): return true
        default: return false
        }
    }
}
