import SwiftUI
import DesignSystem

public enum RuleFormat {
    /// "9:41" today, weekday within a week, otherwise "Sep 28".
    public static func when(_ d: Date, now: Date = .now, calendar: Calendar = .current) -> String {
        if calendar.isDate(d, inSameDayAs: now) { return d.formatted(.dateTime.hour().minute()) }
        if let days = calendar.dateComponents([.day], from: d, to: now).day, days < 7, days >= 0 {
            return d.formatted(.dateTime.weekday(.abbreviated))
        }
        return d.formatted(.dateTime.month(.abbreviated).day())
    }
}

/// Form row: fixed-width caption on the left, content on the right, optional inline error underneath.
public struct RuleFieldRow<Content: View>: View {
    @Environment(\.palette) private var p
    let title: String
    let error: String?
    let divider: Bool
    let content: Content
    public init(_ title: String, error: String? = nil, divider: Bool = true, @ViewBuilder content: () -> Content) {
        self.title = title; self.error = error; self.divider = divider; self.content = content()
    }
    public var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 12) {
                Text(title).font(.inboxBody(17)).foregroundStyle(p.secondary).frame(width: 76, alignment: .leading)
                content.font(.inboxBody(17)).foregroundStyle(p.text).frame(maxWidth: .infinity, alignment: .leading)
            }
            if let error {
                Text(error).font(.inboxBody(14, relativeTo: .footnote)).foregroundStyle(p.destructive)
                    .padding(.leading, 88).accessibilityLabel("Error: \(error)")
            }
        }
        .padding(.horizontal, 16).padding(.vertical, 8).frame(minHeight: 48)
        .contentShape(Rectangle())
        .overlay(alignment: .bottom) { if divider { Rectangle().fill(p.hairline).frame(height: 0.5).padding(.leading, 16) } }
    }
}

/// Row with a title, optional caption and a switch.
public struct RuleSwitchRow: View {
    @Environment(\.palette) private var p
    let title: String
    let caption: String?
    let divider: Bool
    @Binding var isOn: Bool
    public init(_ title: String, caption: String? = nil, divider: Bool = true, isOn: Binding<Bool>) {
        self.title = title; self.caption = caption; self.divider = divider; _isOn = isOn
    }
    public var body: some View {
        HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.inboxBody(17)).foregroundStyle(p.text)
                if let caption { Text(caption).font(.inboxBody(14, relativeTo: .subheadline)).foregroundStyle(p.secondary) }
            }
            Spacer(minLength: 8)
            SquareSwitch(isOn: $isOn).accessibilityLabel(title)
        }
        .padding(.horizontal, 16).padding(.vertical, 4).frame(minHeight: 52)
        .overlay(alignment: .bottom) { if divider { Rectangle().fill(p.hairline).frame(height: 0.5).padding(.leading, 16) } }
    }
}
