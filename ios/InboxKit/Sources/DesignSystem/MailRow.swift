import SwiftUI

public struct MailRowModel: Sendable, Identifiable, Equatable {
    public let id: String
    public var sender: String
    public var subject: String
    public var preview: String
    public var time: String
    public var isUnread: Bool
    public init(id: String, sender: String, subject: String, preview: String, time: String, isUnread: Bool = false) {
        self.id = id; self.sender = sender; self.subject = subject
        self.preview = preview; self.time = time; self.isUnread = isUnread
    }
}

private struct PreviewLinesKey: EnvironmentKey { static let defaultValue = 2 }
public extension EnvironmentValues {
    /// Appearance → Preview lines (1...4).
    var mailPreviewLines: Int { get { self[PreviewLinesKey.self] } set { self[PreviewLinesKey.self] = newValue } }
}

public enum MailRowMode: Sendable, Equatable { case normal, selecting(selected: Bool) }

public struct MailRow: View {
    @Environment(\.palette) private var p
    @Environment(\.mailPreviewLines) private var previewLines
    @Environment(\.dynamicTypeSize) private var typeSize
    let model: MailRowModel?   // nil renders the skeleton
    let mode: MailRowMode
    public init(_ model: MailRowModel?, mode: MailRowMode = .normal) { self.model = model; self.mode = mode }

    public var body: some View {
        HStack(alignment: .top, spacing: 10) {
            leading.frame(width: 10).padding(.top, 6)
            if let m = model { content(m) } else { skeleton }
        }
        .padding(.leading, 12).padding(.trailing, 20).padding(.vertical, 11)
        .contentShape(Rectangle())
        .overlay(alignment: .bottom) { Rectangle().fill(p.hairline).frame(height: 1).padding(.leading, 32) }
        .accessibilityElement(children: .ignore)
        .accessibilityValue({ if case .selecting(let on) = mode { return on ? "Selected" : "Not selected" } else { return "" } }())
        .accessibilityLabel(model.map { "\($0.isUnread ? "Unread. " : "")\($0.sender). \($0.subject). \($0.time)" } ?? "Loading")
    }

    @ViewBuilder private var leading: some View {
        if case .selecting(let selected) = mode {
            Image(systemName: selected ? "checkmark.circle.fill" : "circle")
                .foregroundStyle(selected ? p.accent : p.hairlineStrong)
        } else if let m = model, m.isUnread {
            Circle().fill(p.accent).frame(width: 9, height: 9)
        } else {
            Color.clear
        }
    }

    private func content(_ m: MailRowModel) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            // Accessibility sizes: the time sits under the sender instead of fighting it for the line.
            if typeSize.isAccessibilitySize {
                VStack(alignment: .leading, spacing: 2) {
                    Text(m.sender).font(.inboxBody(16, medium: m.isUnread)).foregroundStyle(p.text)
                    Text(m.time).font(.inboxBody(14, relativeTo: .subheadline)).foregroundStyle(p.secondary)
                }
            } else {
                HStack {
                    Text(m.sender).font(.inboxBody(16, medium: m.isUnread)).foregroundStyle(p.text).lineLimit(1)
                    Spacer(minLength: 8)
                    Text(m.time).font(.inboxBody(14, relativeTo: .subheadline)).foregroundStyle(p.secondary)
                }
            }
            Text(m.subject).font(.inboxBody(15)).foregroundStyle(p.text).lineLimit(typeSize.isAccessibilitySize ? 3 : 1)
            if !m.preview.isEmpty { Text(m.preview).font(.inboxBody(15)).foregroundStyle(p.secondary).lineLimit(previewLines) }
        }
    }

    private var skeleton: some View {
        VStack(alignment: .leading, spacing: 8) {
            bar(0.45); bar(0.8); bar(0.65)
        }
    }
    private func bar(_ fraction: CGFloat) -> some View {
        GeometryReader { g in RoundedRectangle(cornerRadius: 5).fill(p.hairline).frame(width: g.size.width * fraction, height: 11) }
            .frame(height: 11)
    }
}
