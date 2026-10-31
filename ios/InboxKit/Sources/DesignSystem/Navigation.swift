import SwiftUI
import Observation

/// Large page title as drawn on the boards: 30 bold, tight tracking, 20 pt leading, optional secondary subline.
public struct PageTitle: View {
    @Environment(\.palette) private var p
    @Environment(\.dynamicTypeSize) private var typeSize
    let title: String
    let subtitle: String?
    public init(_ title: String, subtitle: String? = nil) { self.title = title; self.subtitle = subtitle }
    public var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(title).font(.inboxHeadingBold(30, relativeTo: .largeTitle)).tracking(-0.75).foregroundStyle(p.text)
                .lineSpacing(3)
                // Accessibility sizes (H-5): break between words only; a single long word scales down instead of splitting.
                .lineLimit(typeSize.isAccessibilitySize ? 2 : nil)
                .minimumScaleFactor(typeSize.isAccessibilitySize ? 0.55 : 1)
            if let subtitle { Text(subtitle).font(.inboxBody(14.5)).monospacedDigit().foregroundStyle(p.secondary) }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.leading, 20).padding(.trailing, 20).padding(.top, 14).padding(.bottom, 12)
        .accessibilityElement(children: .combine).accessibilityAddTraits(.isHeader)
    }
}

/// Plain accent chevron + the previous screen's title in place of the system glass back button.
public struct InboxBackButton: ToolbarContent {
    let title: String
    let action: () -> Void
    public init(_ title: String, action: @escaping () -> Void) { self.title = title; self.action = action }
    public var body: some ToolbarContent {
        ToolbarItem(placement: .topBarLeading) { BackLabel(title: title, action: action) }
            .inboxFlat()
    }
}

struct BackLabel: View {
    @Environment(\.palette) private var p
    let title: String
    let action: () -> Void
    var body: some View {
        Button(action: action) {
            HStack(spacing: 4) {
                Image(systemName: "chevron.left").font(.system(size: 19, weight: .semibold))
                Text(title).font(.inboxBody(17, medium: true)).lineLimit(1).fixedSize()
            }
            .foregroundStyle(p.accentText).frame(minHeight: 44).contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Back, \(title)")
    }
}

public extension ToolbarContent {
    /// The glass capsule behind toolbar items is switched off for the whole app (`UIDesignRequiresCompatibility` in Info.plist):
    /// per-item `sharedBackgroundVisibility` needs an availability check inside the toolbar builder, and that links against a
    /// SwiftUI symbol the device SDK doesn't export at an iOS 17 deployment target (the Release archive failed to link).
    func inboxFlat() -> Self { self }
}

/// Accent text-style toolbar button (Cancel, Done, Edit): no capsule, TA colour.
public struct InboxToolbarButton: View {
    @Environment(\.palette) private var p
    let title: String
    let action: () -> Void
    public init(_ title: String, action: @escaping () -> Void) { self.title = title; self.action = action }
    public var body: some View {
        Button(title, action: action).font(.inboxBody(17, medium: true)).lineLimit(1).fixedSize().foregroundStyle(p.accentText).buttonStyle(.plain).frame(minHeight: 44)
    }
}

public extension View {
    /// Hides the system back button; pair with `InboxBackButton` in a `.toolbar`.
    func inboxNavigationChrome() -> some View {
        navigationBarBackButtonHidden().navigationBarTitleDisplayMode(.inline).toolbarBackground(.hidden, for: .navigationBar)
    }
}

/// Centred nav title as drawn on the boards: Semibold 16.5 in `text` (the system title renders pure black / white).
private struct CentredTitle: ToolbarContent {
    @Environment(\.palette) private var p
    let title: String
    var body: some ToolbarContent {
        ToolbarItem(placement: .principal) {
            Text(title).font(.inboxHeading(16.5, relativeTo: .headline)).foregroundStyle(p.text).lineLimit(1).truncationMode(.tail)
                .accessibilityAddTraits(.isHeader)
        }
    }
}

public extension View {
    /// Inline nav title in the palette's `text` colour (Semibold 16.5), plus the accessibility title.
    func inboxCentredTitle(_ title: String) -> some View {
        navigationTitle(title).navigationBarTitleDisplayMode(.inline).toolbar { CentredTitle(title: title) }
    }
}

/// DD-19: when the back label and the centred title would sit closer than 12 pt, the label becomes "Back".
public enum BackLabelFit {
    public static let minGap: CGFloat = 12
    /// Leading inset + chevron + gap before the label text.
    static let labelOrigin: CGFloat = 16 + 17 + 4

    public static func label(_ back: String, title: String, barWidth: CGFloat) -> String {
        collides(back: back, title: title, barWidth: barWidth) ? "Back" : back
    }

    public static func collides(back: String, title: String, barWidth: CGFloat) -> Bool {
        #if canImport(UIKit)
        guard barWidth > 0, !title.isEmpty, back != "Back" else { return false }
        let backW = textWidth(back, face: InboxFont.bodyMedium, size: inboxScaled(17, .body), weight: .medium)
        let titleW = min(textWidth(title, face: InboxFont.heading, size: inboxScaled(16.5, .headline), weight: .semibold), barWidth)
        return (barWidth - titleW) / 2 - (labelOrigin + backW) < minGap
        #else
        return false
        #endif
    }

    #if canImport(UIKit)
    private static func textWidth(_ s: String, face: String, size: CGFloat, weight: UIFont.Weight) -> CGFloat {
        let font = UIFont(name: face, size: size) ?? UIFont.systemFont(ofSize: size, weight: weight)
        return ceil((s as NSString).size(withAttributes: [.font: font]).width)
    }
    #endif
}

private struct PushedChrome: ViewModifier {
    @Environment(\.dismiss) private var dismiss
    @State private var barWidth: CGFloat = 0
    let title: String
    let backTitle: String
    func body(content: Content) -> some View {
        content
            .background(GeometryReader { g in
                Color.clear.onAppear { barWidth = g.size.width }.onChange(of: g.size.width) { _, w in barWidth = w }
            })
            .inboxCentredTitle(title)
            .inboxNavigationChrome()
            .toolbar { InboxBackButton(BackLabelFit.label(backTitle, title: title, barWidth: barWidth)) { dismiss() } }
    }
}

public extension View {
    /// Pushed screen: small centred title, plain accent "‹ Parent" back button (no glass).
    func inboxPushed(title: String, back: String) -> some View { modifier(PushedChrome(title: title, backTitle: back)) }
}

/// Reduce Motion: the toast fades instead of sliding, and nothing animates.
private struct ToastMotion<V: Equatable>: ViewModifier {
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    let value: V
    func body(content: Content) -> some View {
        content
            .transition(reduceMotion ? .opacity : .move(edge: .bottom).combined(with: .opacity))
            .animation(reduceMotion ? nil : .easeOut(duration: 0.2), value: value)
    }
}

public extension View {
    /// Transition + animation for a toast that appears when `value` changes.
    func toastMotion<V: Equatable>(_ value: V) -> some View { modifier(ToastMotion(value: value)) }
}

/// Screens that replace the tab bar (Businesses edit mode on Mail) set this; the app shell hides the bar while it is true.
@Observable
public final class ChromeState {
    public var hidesTabBar = false
    public init() {}
}
