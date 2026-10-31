import SwiftUI

public enum PrimaryButtonStyle: Sendable { case primary, secondary, destructive, plain }

public struct PrimaryButton: View {
    @Environment(\.palette) private var p
    @Environment(\.isEnabled) private var isEnabled
    let title: String
    let style: PrimaryButtonStyle
    let action: () -> Void
    /// DD-1: while disabled, a button whose label tells the person something (e.g. "Delete everything" until the code matches)
    /// uses fill `field` + label `secondary` at full opacity instead of the dimmed fill.
    let lockedWhenDisabled: Bool
    /// Extra space above and below the label for screens that lay the button out at accessibility sizes (the label may wrap to 2 lines).
    let verticalPadding: CGFloat
    public init(_ title: String, style: PrimaryButtonStyle = .primary, verticalPadding: CGFloat = 0, lockedWhenDisabled: Bool = false, action: @escaping () -> Void = {}) {
        self.title = title; self.style = style; self.verticalPadding = verticalPadding; self.lockedWhenDisabled = lockedWhenDisabled; self.action = action
    }
    public var body: some View {
        Button(action: action) {
            Text(title)
                .font(.inboxBody(17, medium: true))
                .foregroundStyle(fg)
                .multilineTextAlignment(.center)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.vertical, verticalPadding)
                .frame(maxWidth: .infinity, minHeight: 52)
                                .background(RoundedRectangle(cornerRadius: 14, style: .continuous).fill(bg))
                .overlay { if style == .secondary { RoundedRectangle(cornerRadius: 14, style: .continuous).strokeBorder(p.accent, lineWidth: 1.5) } }
        }
        .buttonStyle(UndimmedPlainStyle(undimmed: lockedWhenDisabled))
    }
    private var locked: Bool { lockedWhenDisabled && !isEnabled }
    private var fg: Color {
        if locked { return p.secondary }
        return switch style {
        case .primary: p.onAccent
        case .secondary: p.accentText
        case .destructive: p.hexes.destructive == 0xD92D20 ? .white : p.onAccent
        case .plain: p.accentText
        }
    }
    private var bg: Color {
        if locked { return p.field }
        return switch style {
        case .primary: p.accentFill
        case .secondary: Color.clear
        case .destructive: p.destructive
        case .plain: Color.clear
        }
    }
}

/// `.plain`, or (when `undimmed`) a style that does not fade a disabled label.
private struct UndimmedPlainStyle: PrimitiveButtonStyle {
    let undimmed: Bool
    @ViewBuilder func makeBody(configuration: Configuration) -> some View {
        if undimmed { Button(role: configuration.role) { configuration.trigger() } label: { configuration.label }.buttonStyle(UndimmedButtonStyleShared()) }
        else { Button(role: configuration.role) { configuration.trigger() } label: { configuration.label }.buttonStyle(.plain) }
    }
}
private struct UndimmedButtonStyleShared: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View { configuration.label }
}

/// Header for bottom sheets: grabber, title, optional trailing action.
public struct SheetHeader: View {
    @Environment(\.palette) private var p
    let title: String
    let trailingTitle: String?
    let onTrailing: () -> Void
    public init(_ title: String, trailingTitle: String? = "Done", onTrailing: @escaping () -> Void = {}) {
        self.title = title; self.trailingTitle = trailingTitle; self.onTrailing = onTrailing
    }
    public var body: some View {
        VStack(spacing: 8) {
            Capsule().fill(p.hairlineStrong).frame(width: 36, height: 5).padding(.top, 8)
            HStack {
                Text(title).font(.inboxHeading(20)).foregroundStyle(p.text)
                Spacer()
                if let trailingTitle {
                    Button(trailingTitle, action: onTrailing)
                        .font(.inboxBody(17, medium: true)).foregroundStyle(p.accentText).frame(minHeight: 44)
                }
            }
            .padding(.horizontal, 16)
        }
    }
}
