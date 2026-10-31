#if os(iOS)
import SwiftUI
import WebKit

/// Owns the locked-down `WKWebView` (JS off, non-persistent store, deny-all content rules, cid scheme).
/// Separate from the SwiftUI wrapper so tests can drive exactly the production configuration.
@MainActor
public final class MessageWebLoader: NSObject, WKNavigationDelegate {
    public let webView: WKWebView
    public var onOpenLink: (URL) -> Void
    public var onHeight: (CGFloat) -> Void = { _ in }
    private let handler: CIDSchemeHandler
    private var finish: CheckedContinuation<Void, Never>?
    private var heightObservation: NSKeyValueObservation?
    private var generation = 0

    public init(resolveCID: @escaping @Sendable (String) async -> CIDResource? = { _ in nil },
                onOpenLink: @escaping (URL) -> Void = { _ in }) {
        handler = CIDSchemeHandler(resolver: resolveCID)
        self.onOpenLink = onOpenLink
        let cfg = WKWebViewConfiguration()
        cfg.websiteDataStore = .nonPersistent()
        cfg.defaultWebpagePreferences.allowsContentJavaScript = false
        cfg.preferences.javaScriptCanOpenWindowsAutomatically = false
        cfg.mediaTypesRequiringUserActionForPlayback = .all
        cfg.allowsInlineMediaPlayback = false
        cfg.dataDetectorTypes = []
        cfg.setURLSchemeHandler(handler, forURLScheme: HTMLDocument.cidScheme)
        webView = WKWebView(frame: CGRect(x: 0, y: 0, width: 390, height: 1), configuration: cfg)
        super.init()
        webView.navigationDelegate = self
        webView.overrideUserInterfaceStyle = .light
        webView.isOpaque = false
        webView.backgroundColor = .white
        webView.scrollView.backgroundColor = .white
        webView.scrollView.isScrollEnabled = false
        webView.scrollView.bounces = false
        heightObservation = webView.scrollView.observe(\.contentSize, options: [.new]) { [weak self] _, change in
            guard let h = change.newValue?.height else { return }
            Task { @MainActor in self?.onHeight(h) }
        }
    }

    /// Installs the rule list *before* loading, so nothing can fetch ahead of it. Fails closed: if the rules can't
    /// be compiled nothing is loaded and the error is thrown.
    public func load(html: String, allowImages: Bool, textScalePercent: Int = 100) async throws {
        let rules = try await ContentRules.compile(allowImages: allowImages)
        let ucc = webView.configuration.userContentController
        ucc.removeAllContentRuleLists()
        ucc.add(rules)
        generation += 1
        finish?.resume(); finish = nil
        webView.loadHTMLString(HTMLDocument.prepare(html, allowImages: allowImages, textScalePercent: textScalePercent), baseURL: nil)
        await withCheckedContinuation { finish = $0 }
    }

    private func complete() { finish?.resume(); finish = nil; onHeight(webView.scrollView.contentSize.height) }

    // MARK: WKNavigationDelegate

    public func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction,
                        preferences: WKWebpagePreferences) async -> (WKNavigationActionPolicy, WKWebpagePreferences) {
        preferences.allowsContentJavaScript = false
        let url = action.request.url
        let scheme = url?.scheme?.lowercased()
        if action.navigationType == .linkActivated {
            if let url, scheme == "http" || scheme == "https" { onOpenLink(url) }
            return (.cancel, preferences)
        }
        // Only our own initial document (and srcdoc/cid/data) may navigate; remote documents never.
        let ok = url == nil || ["about", "data", HTMLDocument.cidScheme].contains(scheme ?? "")
        return (ok ? .allow : .cancel, preferences)
    }

    public func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) { complete() }
    public func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: any Error) { complete() }
    public func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: any Error) { complete() }
}

/// SwiftUI wrapper. The web view never scrolls; the parent `ScrollView` does, using the reported `height`.
public struct MessageWebView: UIViewRepresentable {
    let html: String
    let allowImages: Bool
    let resolveCID: @Sendable (String) async -> CIDResource?
    @Binding var height: CGFloat
    let onOpenLink: (URL) -> Void
    @Environment(\.dynamicTypeSize) private var typeSize

    public init(html: String, allowImages: Bool, height: Binding<CGFloat>,
                resolveCID: @escaping @Sendable (String) async -> CIDResource? = { _ in nil },
                onOpenLink: @escaping (URL) -> Void) {
        self.html = html; self.allowImages = allowImages; _height = height
        self.resolveCID = resolveCID; self.onOpenLink = onOpenLink
    }

    @MainActor public final class Coordinator {
        let loader: MessageWebLoader
        var loaded: String?
        init(_ l: MessageWebLoader) { loader = l }
    }

    public func makeCoordinator() -> Coordinator { Coordinator(MessageWebLoader(resolveCID: resolveCID, onOpenLink: onOpenLink)) }

    public func makeUIView(context: Context) -> WKWebView { context.coordinator.loader.webView }

    public func updateUIView(_ view: WKWebView, context: Context) {
        let c = context.coordinator
        c.loader.onOpenLink = onOpenLink
        let h = $height
        c.loader.onHeight = { new in if abs(h.wrappedValue - new) > 0.5 { h.wrappedValue = new } }
        let pct = HTMLDocument.textScalePercent(for: typeSize)
        let key = "\(allowImages)|\(pct)|\(html)"
        guard c.loaded != key else { return }
        c.loaded = key
        Task { @MainActor in try? await c.loader.load(html: html, allowImages: allowImages, textScalePercent: pct) }
    }
}

extension HTMLDocument {
    /// UIFontMetrics scale of `size` against Large, as a percent (100 at Large, capped at 200).
    public static func textScalePercent(for size: DynamicTypeSize) -> Int {
        let category: UIContentSizeCategory
        switch size {
        case .xSmall: category = .extraSmall
        case .small: category = .small
        case .medium: category = .medium
        case .large: category = .large
        case .xLarge: category = .extraLarge
        case .xxLarge: category = .extraExtraLarge
        case .xxxLarge: category = .extraExtraExtraLarge
        case .accessibility1: category = .accessibilityMedium
        case .accessibility2: category = .accessibilityLarge
        case .accessibility3: category = .accessibilityExtraLarge
        case .accessibility4: category = .accessibilityExtraExtraLarge
        case .accessibility5: category = .accessibilityExtraExtraExtraLarge
        @unknown default: category = .large
        }
        let scaled = UIFontMetrics.default.scaledValue(for: 100, compatibleWith: UITraitCollection(preferredContentSizeCategory: category))
        return min(max(Int(scaled.rounded()), 100), 200)
    }
}
#endif
