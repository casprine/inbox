import Testing
import Foundation
import Network
import WebKit
import SwiftUI
@testable import Web

/// Loopback HTTP server that counts every connection and request it receives. Because WKWebView's network
/// stack lives in another process, a `URLProtocol` cannot observe its traffic; instead the hostile fixture points
/// every resource at this server, and a locked-down view must leave both counters at zero. A control run with
/// the protections off must hit the server (otherwise a zero would prove nothing).
final class LoopbackServer: @unchecked Sendable {
    private let listener: NWListener
    private let paths = Mutex<[String]>([])
    private let connections = Mutex<Int>(0)
    private(set) var port: UInt16 = 0
    var requestedPaths: [String] { paths.withLock { $0 } }
    var connectionCount: Int { connections.withLock { $0 } }

    init() throws { listener = try NWListener(using: .tcp, on: .any) }

    func start() async throws {
        listener.newConnectionHandler = { [self] conn in
            connections.withLock { $0 += 1 }
            conn.start(queue: .global())
            conn.receive(minimumIncompleteLength: 1, maximumLength: 8192) { [self] data, _, _, _ in
                if let data, let line = String(data: data, encoding: .utf8)?.split(separator: "\r\n").first {
                    let parts = line.split(separator: " ")
                    if parts.count > 1 { paths.withLock { $0.append(String(parts[1])) } }
                }
                let resp = "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
                conn.send(content: Data(resp.utf8), completion: .contentProcessed { _ in conn.cancel() })
            }
        }
        let flag = Mutex(false)
        try await withCheckedThrowingContinuation { (c: CheckedContinuation<Void, Error>) in
            listener.stateUpdateHandler = { state in
                switch state {
                case .ready: if !flag.withLock({ let o = $0; $0 = true; return o }) { c.resume() }
                case .failed(let e): if !flag.withLock({ let o = $0; $0 = true; return o }) { c.resume(throwing: e) }
                default: break
                }
            }
            listener.start(queue: .global())
        }
        port = listener.port?.rawValue ?? 0
    }

    func stop() { listener.cancel() }

    /// Hostile mail: every vector that could fetch, run or navigate, all aimed at this server.
    func hostileHTML() -> String {
        let o = "http://127.0.0.1:\(port)"
        return """
        <html><head>
        <script src="\(o)/script.js"></script>
        <script>document.documentElement.setAttribute('data-js','ran'); new Image().src='\(o)/js-beacon';</script>
        <link rel="stylesheet" href="\(o)/style.css">
        <meta http-equiv="refresh" content="0;url=\(o)/refresh">
        <style>@import url("\(o)/import.css"); body{background:url(\(o)/bg.png)}</style>
        </head><body onload="document.documentElement.setAttribute('data-js','ran-onload')">
        <img src="\(o)/pixel.gif" onerror="document.documentElement.setAttribute('data-js','ran-onerror')">
        <form action="\(o)/form" method="post"><input name="x" value="1"></form>
        <iframe src="\(o)/frame"></iframe>
        <object data="\(o)/object"></object><embed src="\(o)/embed">
        <video poster="\(o)/poster.png" src="\(o)/video.mp4"></video>
        <a href="\(o)/link">tap</a>
        </body></html>
        """
    }
}

@MainActor
private func jsMarker(_ view: WKWebView) async -> String? {
    await withCheckedContinuation { c in
        view.evaluateJavaScript("document.documentElement.getAttribute('data-js')") { v, _ in c.resume(returning: v as? String) }
    }
}

@MainActor
private final class Nav: NSObject, WKNavigationDelegate {
    var cont: CheckedContinuation<Void, Never>?
    func webView(_ w: WKWebView, didFinish n: WKNavigation!) { cont?.resume(); cont = nil }
    func webView(_ w: WKWebView, didFail n: WKNavigation!, withError e: any Error) { cont?.resume(); cont = nil }
}

@MainActor @Suite struct WebSecurityTests {
    @Test func hostileHTMLMakesNoNetworkRequestsAndRunsNoJS() async throws {
        let server = try LoopbackServer(); try await server.start(); defer { server.stop() }
        let loader = MessageWebLoader()
        try await loader.load(html: server.hostileHTML(), allowImages: false)
        try await Task.sleep(for: .seconds(1))   // meta refresh, lazy loads, timers
        #expect(server.connectionCount == 0, "connections: \(server.requestedPaths)")
        #expect(server.requestedPaths.isEmpty)
        #expect(await jsMarker(loader.webView) == nil, "inline script / event handlers must not run")
        // JS from the app still works (that is how we probe), page scripts do not.
        let sum = try await loader.webView.evaluateJavaScript("1+1") as? Int
        #expect(sum == 2)
    }

    @Test func loadImagesAllowsOnlyImages() async throws {
        let server = try LoopbackServer(); try await server.start(); defer { server.stop() }
        let loader = MessageWebLoader()
        try await loader.load(html: server.hostileHTML(), allowImages: true)
        try await Task.sleep(for: .seconds(1))
        let allowed: Set<String> = ["/pixel.gif", "/bg.png", "/poster.png"]
        #expect(Set(server.requestedPaths).isSubset(of: allowed), "unexpected: \(server.requestedPaths)")
        #expect(server.requestedPaths.contains("/pixel.gif"))
        #expect(await jsMarker(loader.webView) == nil)
    }

    /// Control: the same fixture (minus the page-replacing meta refresh) in a default WKWebView DOES hit the server and DOES run JS, so the zeros above are meaningful.
    @Test func controlUnprotectedWebViewHitsNetworkAndRunsJS() async throws {
        let server = try LoopbackServer(); try await server.start(); defer { server.stop() }
        let view = WKWebView(frame: .init(x: 0, y: 0, width: 390, height: 600), configuration: WKWebViewConfiguration())
        let nav = Nav(); view.navigationDelegate = nav
        await withCheckedContinuation { nav.cont = $0; view.loadHTMLString(server.hostileHTML().replacingOccurrences(of: "http-equiv=\"refresh\"", with: "http-equiv=\"x-none\""), baseURL: nil) }
        try await Task.sleep(for: .seconds(1))
        #expect(server.connectionCount > 0)
        #expect(server.requestedPaths.contains("/script.js") || server.requestedPaths.contains("/pixel.gif"))
        #expect(await jsMarker(view) != nil)
    }

    @Test func ruleListsCompileAndShape() async throws {
        for allow in [false, true] {
            let json = ContentRules.json(allowImages: allow)
            let arr = try JSONSerialization.jsonObject(with: Data(json.utf8)) as? [[String: Any]]
            #expect(arr?.first?["action"] as? [String: String] == ["type": "block"])
            #expect(json.contains("resource-type") == allow)
            _ = try await ContentRules.compile(allowImages: allow)
        }
    }

    @Test func cidImagesResolveThroughSchemeHandler() async throws {
        let png = Data(base64Encoded: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")!
        let asked = Mutex<[String]>([])
        let loader = MessageWebLoader(resolveCID: { cid in asked.withLock { $0.append(cid) }; return CIDResource(data: png, mime: "image/png") })
        try await loader.load(html: #"<body><img id="i" src="cid:Logo@X.com"></body>"#, allowImages: false)
        #expect(asked.withLock { $0 } == ["logo@x.com"])
        let w = try await loader.webView.evaluateJavaScript("document.getElementById('i').naturalWidth") as? Int
        #expect(w == 1)
    }

    @Test func navigationPolicy() async throws {
        final class Action: WKNavigationAction, @unchecked Sendable {
            let u: URL, t: WKNavigationType
            init(_ u: URL, _ t: WKNavigationType) { self.u = u; self.t = t; super.init() }
            override var request: URLRequest { URLRequest(url: u) }
            override var navigationType: WKNavigationType { t }
        }
        let opened = Mutex<[URL]>([])
        let loader = MessageWebLoader(onOpenLink: { u in opened.withLock { $0.append(u) } })
        let prefs = WKWebpagePreferences()
        func decide(_ u: String, _ t: WKNavigationType) async -> WKNavigationActionPolicy {
            await loader.webView(loader.webView, decidePolicyFor: Action(URL(string: u)!, t), preferences: prefs).0
        }
        #expect(await decide("https://example.com/a", .linkActivated) == .cancel)
        #expect(await decide("http://example.com/b", .linkActivated) == .cancel)
        #expect(await decide("mailto:a@b.co", .linkActivated) == .cancel)
        #expect(await decide("javascript:alert(1)", .linkActivated) == .cancel)
        #expect(await decide("https://example.com/c", .other) == .cancel)
        #expect(await decide("https://example.com/f", .formSubmitted) == .cancel)
        #expect(await decide("about:blank", .other) == .allow)
        #expect(opened.withLock { $0.map(\.absoluteString) } == ["https://example.com/a", "http://example.com/b"])
        #expect(prefs.allowsContentJavaScript == false)
    }

    @Test func htmlTextSizeAdjustFollowsDynamicType() {
        #expect(HTMLDocument.textScalePercent(for: .large) == 100)
        #expect(HTMLDocument.textScalePercent(for: .xSmall) == 100)
        #expect(HTMLDocument.textScalePercent(for: .accessibility3) > 150)
        #expect(HTMLDocument.textScalePercent(for: .accessibility5) <= 200)
        #expect(HTMLDocument.prepare("<p>x</p>").contains("-webkit-text-size-adjust:100%"))
        #expect(HTMLDocument.prepare("<p>x</p>", textScalePercent: 190).contains("-webkit-text-size-adjust:190%"))
        #expect(HTMLDocument.prepare("<p>x</p>", textScalePercent: 900).contains("-webkit-text-size-adjust:200%"))
    }

    @Test func htmlPreparation() {
        let a = HTMLDocument.prepare("<html><head><title>x</title></head><body>hi</body></html>")
        #expect(a.contains("color-scheme") && a.contains("viewport"))
        #expect(HTMLDocument.prepare("<p>plain fragment</p>").hasPrefix("<!doctype html>"))
        let c = HTMLDocument.rewriteCIDs(#"<img src="cid:a@b"> <div style="background:url(cid:z)"> <a href="x">cid:notattr</a>"#)
        #expect(c.contains(#"src="inbox-cid://m/a@b""#) && c.contains("url(inbox-cid://m/z)"))
        #expect(HTMLDocument.normalizeCID("<Logo@X.com>") == "logo@x.com")
    }

    @Test func plainTextLinksOnlyHttp() {
        let a = PlainText.attributed("see https://example.com/a?b=1 or mail bob@example.com or call 555-123-4567")
        let links = a.runs.compactMap(\.link).map(\.absoluteString)
        #expect(links == ["https://example.com/a?b=1"])
    }
}
