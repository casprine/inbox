import SwiftUI

// MARK: Status dot / Chip / Switch

public enum StatusKind: Sendable { case ok, warning, error, idle }

public struct StatusDot: View {
    @Environment(\.palette) private var p
    let kind: StatusKind
    public init(_ kind: StatusKind) { self.kind = kind }
    public var body: some View {
        Circle().fill(color).frame(width: 8, height: 8)
            .accessibilityHidden(true)
    }
    private var color: Color {
        switch kind {
        case .ok: Color.hex(0x3E8E5A)
        case .warning: p.warning
        case .error: p.accent
        case .idle: p.tertiary
        }
    }
}

public struct Chip: View {
    @Environment(\.palette) private var p
    let title: String
    let selected: Bool
    let action: () -> Void
    public init(_ title: String, selected: Bool = false, action: @escaping () -> Void = {}) {
        self.title = title; self.selected = selected; self.action = action
    }
    public var body: some View {
        Button(action: action) {
            Text(title)
                .font(.inboxBody(14.5, medium: selected))
                .foregroundStyle(selected ? p.onAccent : p.text)
                .padding(.horizontal, 13).frame(height: 32)
                .background(Capsule().fill(selected ? p.accentFill : .clear))
                .overlay { if !selected { Capsule().strokeBorder(p.hairlineStrong, lineWidth: 1) } }
                .frame(minHeight: 44)
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}

/// The rounded-square toggle, 48x28.
public struct SquareSwitch: View {
    @Environment(\.palette) private var p
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Binding var isOn: Bool
    public init(isOn: Binding<Bool>) { _isOn = isOn }
    public var body: some View {
        Button { isOn.toggle() } label: {
            RoundedRectangle(cornerRadius: 8, style: .continuous)
                .fill(isOn ? p.accent : p.hairlineStrong)
                .frame(width: 48, height: 28)
                .overlay(alignment: isOn ? .trailing : .leading) {
                    RoundedRectangle(cornerRadius: 6, style: .continuous)
                        .fill(p.knob)
                        .frame(width: 22, height: 22).padding(3)
                        .shadow(color: .black.opacity(0.25), radius: 1, y: 1)
                }
                .animation(reduceMotion ? nil : .easeOut(duration: 0.15), value: isOn)
                .frame(minWidth: 48, minHeight: 44)
        }
        .buttonStyle(.plain)
        .accessibilityValue(isOn ? "On" : "Off")
        .accessibilityAddTraits(.isToggle)
    }
}

// MARK: Inset groups

public struct InsetGroup<Content: View>: View {
    @Environment(\.palette) private var p
    let header: String?
    let content: Content
    public init(_ header: String? = nil, @ViewBuilder content: () -> Content) {
        self.header = header; self.content = content()
    }
    public var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if let header {
                Text(header.uppercased()).font(.inboxBody(12.5, medium: true)).tracking(0.5).foregroundStyle(p.secondary)
                    .padding(.horizontal, 16)
            }
            VStack(spacing: 0) { content }
                .background(RoundedRectangle(cornerRadius: 12, style: .continuous).fill(p.card))
                .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
        }
    }
}

public struct InsetRow<Trailing: View>: View {
    @Environment(\.palette) private var p
    @Environment(\.dynamicTypeSize) private var typeSize
    let title: String
    let subtitle: String?
    let showsDivider: Bool
    let systemImage: String?
    let trailing: Trailing
    public init(_ title: String, subtitle: String? = nil, showsDivider: Bool = true, systemImage: String? = nil,
                @ViewBuilder trailing: () -> Trailing = { EmptyView() }) {
        self.title = title; self.subtitle = subtitle; self.showsDivider = showsDivider; self.systemImage = systemImage; self.trailing = trailing()
    }
    private var titles: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(title).font(.inboxBody(16.5)).foregroundStyle(p.text).fixedSize(horizontal: false, vertical: true)
            if let subtitle { Text(subtitle).font(.inboxBody(14, relativeTo: .subheadline)).foregroundStyle(p.secondary).fixedSize(horizontal: false, vertical: true) }
        }
    }
    public var body: some View {
        HStack(spacing: 12) {
            if let systemImage { Image(systemName: systemImage).font(.system(size: 20)).foregroundStyle(p.accent).frame(width: 24) }
            // Accessibility sizes: the value drops under the title instead of squeezing both into one line.
            if typeSize.isAccessibilitySize {
                VStack(alignment: .leading, spacing: 4) { titles; trailing }
                Spacer(minLength: 0)
            } else {
                titles
                Spacer(minLength: 8)
                trailing
            }
        }
        .padding(.horizontal, 16).padding(.vertical, 11).frame(minHeight: 56)
        .contentShape(Rectangle())
        .overlay(alignment: .bottom) {
            if showsDivider { Rectangle().fill(p.hairline).frame(height: 1).padding(.leading, 16) }
        }
        .accessibilityElement(children: .combine)
    }
}

// MARK: Progress, banner, empty state

public struct InboxProgressBar: View {
    @Environment(\.palette) private var p
    let value: Double
    public init(value: Double) { self.value = min(max(value, 0), 1) }
    public var body: some View {
        GeometryReader { g in
            ZStack(alignment: .leading) {
                Capsule().fill(p.hairline)
                Capsule().fill(p.accent).frame(width: g.size.width * value)
            }
        }
        .frame(height: 6)
        .accessibilityElement()
        .accessibilityValue("\(Int(value * 100)) percent")
    }
}

public struct Banner: View {
    @Environment(\.palette) private var p
    let kind: StatusKind
    let title: String
    let message: String?
    let actionTitle: String?
    let systemImage: String?
    let inline: Bool
    let action: () -> Void
    /// `systemImage` swaps the status dot for an 18 pt icon in the status colour (boards MS04, GM02, GM03); with
    /// `inline` the bold lead and the sentence run on in one paragraph.
    public init(_ kind: StatusKind = .warning, title: String, message: String? = nil, actionTitle: String? = nil,
                systemImage: String? = nil, inline: Bool = false, action: @escaping () -> Void = {}) {
        self.kind = kind; self.title = title; self.message = message; self.actionTitle = actionTitle
        self.systemImage = systemImage; self.inline = inline; self.action = action
    }
    private var tint: Color {
        switch kind {
        case .ok: Color.hex(0x3E8E5A)
        case .warning: p.warning
        case .error: p.destructive
        case .idle: p.secondary
        }
    }
    public var body: some View {
        HStack(alignment: .top, spacing: 12) {
            if let systemImage { Image(systemName: systemImage).font(.system(size: 18)).foregroundStyle(tint).padding(.top, 1) }
            else { StatusDot(kind).padding(.top, 7) }
            if inline, let message {
                (Text(title + " ").font(.inboxBody(15, medium: true)).foregroundColor(p.text) + Text(message).font(.inboxBody(15)).foregroundColor(p.text))
                    .fixedSize(horizontal: false, vertical: true)
            } else {
                VStack(alignment: .leading, spacing: 2) {
                    Text(title).font(.inboxBody(15, medium: true)).foregroundStyle(p.text)
                    if let message { Text(message).font(.inboxBody(14, relativeTo: .subheadline)).foregroundStyle(p.secondary) }
                }
            }
            Spacer(minLength: 8)
            if let actionTitle {
                Button(actionTitle, action: action)
                    .font(.inboxBody(15, medium: true)).foregroundStyle(p.accentText)
                    .frame(minHeight: 44)
            }
        }
        .padding(.horizontal, 14).padding(.vertical, 12)
        .background(RoundedRectangle(cornerRadius: 12, style: .continuous).fill(p.card))
        .accessibilityElement(children: .combine)
    }
}

public struct EmptyState: View {
    @Environment(\.palette) private var p
    let title: String
    let message: String
    let actionTitle: String?
    let action: () -> Void
    public init(title: String, message: String, actionTitle: String? = nil, action: @escaping () -> Void = {}) {
        self.title = title; self.message = message; self.actionTitle = actionTitle; self.action = action
    }
    public var body: some View {
        VStack(spacing: 8) {
            Text(title).font(.inboxHeading(22)).foregroundStyle(p.text)
            Text(message).font(.inboxBody(16)).foregroundStyle(p.secondary).multilineTextAlignment(.center)
            if let actionTitle {
                PrimaryButton(actionTitle, style: .secondary, action: action).padding(.top, 8)
            }
        }
        .padding(32).frame(maxWidth: .infinity)
    }
}

// MARK: Toast

public struct Toast: View {
    @Environment(\.palette) private var p
    let message: String
    let undoTitle: String?
    let onUndo: () -> Void
    public init(_ message: String, undoTitle: String? = "Undo", onUndo: @escaping () -> Void = {}) {
        self.message = message; self.undoTitle = undoTitle; self.onUndo = onUndo
    }
    public var body: some View {
        HStack(spacing: 16) {
            Text(message).font(.inboxBody(15)).foregroundStyle(p.ground)
            Spacer(minLength: 8)
            if let undoTitle {
                Button(undoTitle, action: onUndo)
                    .font(.inboxBody(15, medium: true)).foregroundStyle(p == .dark ? Palette.light.accentText : Palette.dark.accentText)
                    .frame(minHeight: 44)
            }
        }
        .padding(.horizontal, 16).padding(.vertical, 4)
        .background(RoundedRectangle(cornerRadius: 12, style: .continuous).fill(p.text))
        .accessibilityElement(children: .contain)
    }
}
