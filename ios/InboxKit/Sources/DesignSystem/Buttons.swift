import SwiftUI

public enum PrimaryButtonStyle: Sendable { case primary, secondary, destructive, plain }

public struct PrimaryButton: View {
    @Environment(\.palette) private var p
    let title: String
    let style: PrimaryButtonStyle
    let action: () -> Void
    /// Extra space above and below the label for screens that lay the button out at accessibility sizes (the label may wrap to 2 lines).
    let verticalPadding: CGFloat
    public init(_ title: String, style: PrimaryButtonStyle = .primary, verticalPadding: CGFloat = 0, action: @escaping () -> Void = {}) {
        self.title = title; self.style = style; self.verticalPadding = verticalPadding; self.action = action
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
        .buttonStyle(.plain)
    }
    private var fg: Color {
        switch style {
        case .primary: p.onAccent
        case .secondary: p.accentText
        case .destructive: p.hexes.destructive == 0xD92D20 ? .white : p.onAccent
        case .plain: p.accentText
        }
    }
    private var bg: Color {
        switch style {
        case .primary: p.accentFill
        case .secondary: .clear
        case .destructive: p.destructive
        case .plain: .clear
        }
    }
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
                Text(title).font(.inboxHeading(21)).foregroundStyle(p.text)
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
