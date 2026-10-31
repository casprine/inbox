import SwiftUI
import DesignSystem
import Domain

/// MS06: bottom sheet with the message's secondary actions.
struct MoreSheet: View {
    @Environment(\.palette) private var p
    let model: MessageModel
    let close: () -> Void
    let onNavigate: (Route) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Capsule().fill(p.hairline).frame(width: 36, height: 5).frame(maxWidth: .infinity).padding(.top, 8)
            VStack(alignment: .leading, spacing: 2) {
                Text(model.detail?.details.business?.name ?? "").font(.inboxBody(14, relativeTo: .subheadline)).foregroundStyle(p.secondary)
                Text(model.latest?.subject ?? "").font(.inboxHeading(20)).foregroundStyle(p.text).lineLimit(2)
            }
            .padding(16)
            row("Mark as unread", "envelope.badge") { Task { await model.markUnread(); close() } }
            row("Open in Gmail", "arrow.up.right.square") { Task { await openGmail() } }
            row("Copy sender address", "doc.on.doc") {
                UIPasteboard.general.string = model.detail?.message.fromAddr
                model.notice = "Address copied"; close()
            }
            row("Details", "info.circle", subtitle: "Rule, labels and Gmail sync") { onNavigate(.details(model.id)) }
            row("View original", "curlybraces", subtitle: "Headers and raw source") { onNavigate(.original(model.id)) }
            Spacer(minLength: 0)
        }
        .background(p.card)
    }

    private func row(_ title: String, _ icon: String, subtitle: String? = nil, _ action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 14) {
                Image(systemName: icon).frame(width: 24).foregroundStyle(p.secondary)
                VStack(alignment: .leading, spacing: 0) {
                    Text(title).font(.inboxBody(17)).foregroundStyle(p.text)
                    if let subtitle { Text(subtitle).font(.inboxBody(13)).foregroundStyle(p.secondary) }
                }
                Spacer()
            }
            .padding(.horizontal, 16).frame(minHeight: 52).contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .overlay(alignment: .bottom) { Rectangle().fill(p.hairline).frame(height: 0.5).padding(.leading, 54) }
    }

    @MainActor private func openGmail() async {
        let web = GmailLinks.web(gmailID: model.detail?.details.gmailId)
        close()
        if await !UIApplication.shared.open(GmailLinks.app) { await UIApplication.shared.open(web) }
    }
}
