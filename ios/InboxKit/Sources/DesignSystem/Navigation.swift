import SwiftUI
import Observation

/// Large page title as drawn on the boards: 32 bold, tight tracking, 20 pt leading, optional secondary subline.
public struct PageTitle: View {
    @Environment(\.palette) private var p
    let title: String
    let subtitle: String?
    public init(_ title: String, subtitle: String? = nil) { self.title = title; self.subtitle = subtitle }
    public var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(title).font(.inboxHeading(32, relativeTo: .largeTitle)).tracking(-0.64).foregroundStyle(p.text)
                .lineSpacing(3)
            if let subtitle { Text(subtitle).font(.inboxBody(15)).foregroundStyle(p.secondary) }
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
                Text(title).font(.inboxBody(17)).lineLimit(1).fixedSize()
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
        Button(title, action: action).font(.inboxBody(17)).lineLimit(1).fixedSize().foregroundStyle(p.accentText).buttonStyle(.plain).frame(minHeight: 44)
    }
}

public extension View {
    /// Hides the system back button; pair with `InboxBackButton` in a `.toolbar`.
    func inboxNavigationChrome() -> some View {
        navigationBarBackButtonHidden().navigationBarTitleDisplayMode(.inline).toolbarBackground(.hidden, for: .navigationBar)
    }
}

private struct PushedChrome: ViewModifier {
    @Environment(\.dismiss) private var dismiss
    let title: String
    let backTitle: String
    func body(content: Content) -> some View {
        content
            .navigationTitle(title)
            .inboxNavigationChrome()
            .toolbar { InboxBackButton(backTitle) { dismiss() } }
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
