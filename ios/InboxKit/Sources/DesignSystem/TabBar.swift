import SwiftUI

public enum InboxTab: String, CaseIterable, Sendable, Identifiable {
    case mail = "Mail", rules = "Rules", settings = "Settings"
    public var id: String { rawValue }
}

private struct TabIcon: Shape {
    let tab: InboxTab
    func path(in r: CGRect) -> Path {
        var p = Path()
        let s = r.width / 24
        func pt(_ x: CGFloat, _ y: CGFloat) -> CGPoint { CGPoint(x: r.minX + x * s, y: r.minY + y * s) }
        switch tab {
        case .mail:   // tray
            p.move(to: pt(3, 13)); p.addLine(to: pt(6.5, 5)); p.addLine(to: pt(17.5, 5)); p.addLine(to: pt(21, 13))
            p.addLine(to: pt(21, 19)); p.addLine(to: pt(3, 19)); p.closeSubpath()
            p.move(to: pt(3, 13)); p.addLine(to: pt(8.5, 13)); p.addLine(to: pt(10, 15.5)); p.addLine(to: pt(14, 15.5)); p.addLine(to: pt(15.5, 13)); p.addLine(to: pt(21, 13))
        case .rules:  // sliders
            p.move(to: pt(4, 7)); p.addLine(to: pt(20, 7)); p.move(to: pt(7, 12)); p.addLine(to: pt(17, 12)); p.move(to: pt(10, 17)); p.addLine(to: pt(14, 17))
        case .settings:  // gear-ish ring with ticks
            p.addEllipse(in: CGRect(x: r.minX + 8 * s, y: r.minY + 8 * s, width: 8 * s, height: 8 * s))
            for a in stride(from: 0.0, to: 360.0, by: 45.0) {
                let rad = a * .pi / 180
                p.move(to: pt(12 + 6.5 * cos(rad), 12 + 6.5 * sin(rad)))
                p.addLine(to: pt(12 + 9.5 * cos(rad), 12 + 9.5 * sin(rad)))
            }
        }
        return p
    }
}

/// Three-tab bar, 84 pt with the home-indicator inset: 26 pt icon over an 11 pt label, a 30x3 bar flush with the top hairline.
public struct InboxTabBar: View {
    @Environment(\.palette) private var p
    @Binding var selection: InboxTab
    public init(selection: Binding<InboxTab>) { _selection = selection }
    public var body: some View {
        HStack(spacing: 0) {
            ForEach(InboxTab.allCases) { tab in
                let on = selection == tab
                Button { selection = tab } label: {
                    VStack(spacing: 3) {
                        TabIcon(tab: tab).stroke(on ? p.accent : p.secondary, style: StrokeStyle(lineWidth: 1.6, lineCap: .round, lineJoin: .round))
                            .frame(width: 26, height: 26)
                        Text(tab.rawValue).font(.inboxBody(11, medium: true, relativeTo: .caption2))
                            .foregroundStyle(on ? p.accentText : p.secondary)
                    }
                    .padding(.top, 8)
                    .frame(maxWidth: .infinity, minHeight: 49, alignment: .top)
                    .overlay(alignment: .top) {
                        if on {
                            UnevenRoundedRectangle(bottomLeadingRadius: 3, bottomTrailingRadius: 3).fill(p.accent)
                                .frame(width: 30, height: 3).offset(y: -0.5)
                        }
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityLabel(tab.rawValue)
                .accessibilityAddTraits(on ? .isSelected : [])
            }
        }
        .background(p.ground)
        .overlay(alignment: .top) { Rectangle().fill(p.hairline).frame(height: 1) }
    }
}
