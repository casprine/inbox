#if os(iOS)
import WebKit

public struct CIDResource: Sendable {
    public var data: Data
    public var mime: String
    public init(data: Data, mime: String) { self.data = data; self.mime = mime }
}

/// Serves `inbox-cid://m/<content-id>` subresources. The resolver (API with the bearer token) returns the bytes.
@MainActor
final class CIDSchemeHandler: NSObject, WKURLSchemeHandler {
    private let resolver: @Sendable (String) async -> CIDResource?
    private var tasks: [ObjectIdentifier: Task<Void, Never>] = [:]
    init(resolver: @escaping @Sendable (String) async -> CIDResource?) { self.resolver = resolver }

    func webView(_ webView: WKWebView, start urlSchemeTask: any WKURLSchemeTask) {
        let key = ObjectIdentifier(urlSchemeTask)
        guard let url = urlSchemeTask.request.url, url.scheme == HTMLDocument.cidScheme else {
            urlSchemeTask.didFailWithError(URLError(.badURL)); return
        }
        let cid = HTMLDocument.normalizeCID(String(url.path.dropFirst()))
        let resolver = resolver
        tasks[key] = Task { @MainActor [weak self] in
            let res = await resolver(cid)
            guard let self, self.tasks[key] != nil, !Task.isCancelled else { return }
            self.tasks[key] = nil
            guard let res else { urlSchemeTask.didFailWithError(URLError(.fileDoesNotExist)); return }
            urlSchemeTask.didReceive(URLResponse(url: url, mimeType: res.mime, expectedContentLength: res.data.count, textEncodingName: nil))
            urlSchemeTask.didReceive(res.data)
            urlSchemeTask.didFinish()
        }
    }

    func webView(_ webView: WKWebView, stop urlSchemeTask: any WKURLSchemeTask) {
        tasks.removeValue(forKey: ObjectIdentifier(urlSchemeTask))?.cancel()
    }
}
#endif
