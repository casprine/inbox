import SwiftUI
#if canImport(UIKit)
import UIKit
#endif

/// Open Runde (SIL OFL, D-011) is bundled in the app (`Inbox/Fonts`, registered via UIAppFonts). If a face fails to load we fall back to a system font.
/// Mono is the system monospaced font (GT America Mono is not licensed), so `mono` stays unresolved on purpose.
public enum InboxFont {
    public static let heading = "OpenRunde-Semibold"
    public static let body = "OpenRunde-Regular"
    public static let bodyMedium = "OpenRunde-Medium"
    public static let bold = "OpenRunde-Bold"
    public static let mono = ""

    /// `UIFont(name:)` is a slow lookup that fails every time when a face is missing, and fonts are built in every row's body:
    /// answer once per face.
    private static let installed: [String: Bool] = Dictionary(uniqueKeysWithValues: [heading, body, bodyMedium, bold].map { name in
        #if canImport(UIKit)
        (name, UIFont(name: name, size: 12) != nil)
        #else
        (name, false)
        #endif
    })
    static func available(_ name: String) -> Bool { installed[name] ?? false }
}

/// The Dynamic Type size the current view tree renders at. `ThemeRoot` (via `.inboxTheme`) writes it from the SwiftUI environment
/// before its content builds, so fonts created in view bodies follow both the system setting and test/preview overrides.
public enum InboxType {
    private static let lock = NSLock()
    nonisolated(unsafe) private static var stored: DynamicTypeSize = .large
    /// Written by `ThemeRoot` on the main thread, read by every font helper; locked because tests render views in parallel
    /// (a bare global trips Swift's dynamic exclusivity check in debug builds).
    public static var size: DynamicTypeSize {
        get { lock.lock(); defer { lock.unlock() }; return stored }
        set { lock.lock(); stored = newValue; lock.unlock() }
    }

    #if canImport(UIKit)
    public static func category(_ s: DynamicTypeSize) -> UIContentSizeCategory {
        switch s {
        case .xSmall: .extraSmall; case .small: .small; case .medium: .medium; case .large: .large
        case .xLarge: .extraLarge; case .xxLarge: .extraExtraLarge; case .xxxLarge: .extraExtraExtraLarge
        case .accessibility1: .accessibilityMedium; case .accessibility2: .accessibilityLarge
        case .accessibility3: .accessibilityExtraLarge; case .accessibility4: .accessibilityExtraExtraLarge
        case .accessibility5: .accessibilityExtraExtraExtraLarge
        @unknown default: .large
        }
    }
    #endif
}

/// Fixed design sizes that still follow Dynamic Type.
func inboxScaled(_ size: CGFloat, _ style: Font.TextStyle) -> CGFloat {
    #if canImport(UIKit)
    let category = InboxType.size
    let key = ScaleKey(size: size, style: style, category: category)
    if let hit = ScaleCache.get(key) { return hit }
    let ui: UIFont.TextStyle = switch style {
    case .largeTitle: .largeTitle
    case .title: .title1
    case .title2: .title2
    case .title3: .title3
    case .headline: .headline
    case .subheadline: .subheadline
    case .callout: .callout
    case .footnote: .footnote
    case .caption: .caption1
    case .caption2: .caption2
    default: .body
    }
    let v = UIFontMetrics(forTextStyle: ui).scaledValue(for: size, compatibleWith: UITraitCollection(preferredContentSizeCategory: InboxType.category(category)))
    ScaleCache.set(key, v)
    return v
    #else
    return size
    #endif
}

public extension Font {
    static func inboxHeading(_ size: CGFloat, relativeTo style: Font.TextStyle = .title2) -> Font {
        InboxFont.available(InboxFont.heading)
            ? .custom(InboxFont.heading, size: size, relativeTo: style)
            : .system(size: inboxScaled(size, style), weight: .bold)
    }
    /// Open Runde Bold for the large onboarding and sign-in headings (D4 §2.3: h1 32/36, h2 27/31).
    static func inboxHeadingBold(_ size: CGFloat, relativeTo style: Font.TextStyle = .title2) -> Font {
        InboxFont.available(InboxFont.bold)
            ? .custom(InboxFont.bold, size: size, relativeTo: style)
            : .system(size: inboxScaled(size, style), weight: .bold)
    }
    static func inboxBody(_ size: CGFloat = 17, medium: Bool = false, relativeTo style: Font.TextStyle = .body) -> Font {
        let name = medium ? InboxFont.bodyMedium : InboxFont.body
        return InboxFont.available(name)
            ? .custom(name, size: size, relativeTo: style)
            : .system(size: inboxScaled(size, style), weight: medium ? .medium : .regular)
    }
    static func inboxMono(_ size: CGFloat = 13, relativeTo style: Font.TextStyle = .footnote) -> Font {
        .system(size: inboxScaled(size, style), design: .monospaced)
    }
}

#if canImport(UIKit)
private struct ScaleKey: Hashable { let size: CGFloat; let style: Font.TextStyle; let category: DynamicTypeSize }
/// Scaled sizes are looked up for every text in every row; the answer only changes with the Dynamic Type setting.
private enum ScaleCache {
    private static let lock = NSLock()
    nonisolated(unsafe) private static var values: [ScaleKey: CGFloat] = [:]
    static func get(_ k: ScaleKey) -> CGFloat? { lock.lock(); defer { lock.unlock() }; return values[k] }
    static func set(_ k: ScaleKey, _ v: CGFloat) { lock.lock(); values[k] = v; lock.unlock() }
}
#endif
