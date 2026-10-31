import SwiftUI
import DesignSystem
import Domain
import API

private struct ValueRow: View {
    @Environment(\.palette) private var p
    let title: String
    let value: String
    var divider = true
    var body: some View {
        InsetRow(title, showsDivider: divider) {
            Text(value).font(.inboxBody(17)).foregroundStyle(p.secondary).multilineTextAlignment(.trailing)
        }
    }
}

/// MS07 (Details) and MS09 (Gmail not found, with Try again).
public struct DetailsScreen: View {
    @Environment(\.palette) private var p
    @State private var model: MessageDetailsModel
    public init(env: AppEnvironment, id: UUID) { _model = State(initialValue: MessageDetailsModel(env: env, id: id)) }

    public var body: some View {
        ScrollView {
            if let d = model.detail { content(d) }
            else if let e = model.error {
                EmptyState(title: "Can't load details", message: e, actionTitle: "Try again") { Task { await model.loadDetails() } }.padding(.top, 60)
            } else { ProgressView().frame(maxWidth: .infinity).padding(.top, 80) }
        }
        .background(p.ground)
        .inboxPushed(title: "Details", back: "Message")
        .safeAreaInset(edge: .bottom) {
            if let n = model.notice { Toast(n, undoTitle: nil).padding(.horizontal, 16).padding(.bottom, 8) }
        }
        .task { await model.loadDetails() }
        .task(id: model.notice) {
            guard model.notice != nil else { return }
            try? await Task.sleep(for: .seconds(3)); model.notice = nil
        }
    }

    @ViewBuilder private func content(_ d: MessageDetail) -> some View {
        let gmail = d.details.gmail
        let notFound = gmail?.state == "not_found"
        VStack(alignment: .leading, spacing: 16) {
            if notFound {
                Banner(.warning, title: "Couldn't find this message in Gmail.", message: "It may still be arriving, or a Gmail filter deleted it.")
            }
            InsetGroup("Routing") {
                ValueRow(title: "Business", value: d.details.business?.name ?? "None")
                ValueRow(title: "Address", value: d.details.address.map { $0.split(separator: "@").first.map { "\($0)@" } ?? $0 } ?? "Unknown")
                ValueRow(title: "Matched rule", value: d.details.ruleName ?? "None", divider: false)
            }
            InsetGroup("Gmail") {
                ValueRow(title: "Status", value: MessageFormat.gmailStatus(gmail?.state), divider: notFound || gmail != nil)
                if notFound {
                    ValueRow(title: "Tries", value: gmail?.attempts.map { "\($0) over 15 min" } ?? "6 over 15 min")
                    ValueRow(title: "Last try", value: (gmail?.lastAttemptAt).map { MessageFormat.clock($0) } ?? "Unknown", divider: false)
                } else {
                    ValueRow(title: "Labels", value: d.details.labels.isEmpty ? "None" : d.details.labels.joined(separator: ", "))
                    ValueRow(title: "Archived in Gmail", value: d.message.archived ? "Yes" : "No")
                    ValueRow(title: "Last synced", value: (gmail?.lastSyncedAt).map { MessageFormat.clock($0) } ?? "Not yet", divider: false)
                }
            }
            if notFound {
                PrimaryButton(model.isRetrying ? "Trying…" : "Try again") { Task { await model.retry() } }.disabled(model.isRetrying)
                Text("Labels and archive are applied as soon as Gmail has the message. The copy here is complete either way.")
                    .font(.inboxBody(14, relativeTo: .subheadline)).foregroundStyle(p.secondary)
            }
            InsetGroup("Message") {
                ValueRow(title: "Received", value: MessageFormat.clock(d.details.receivedAt))
                ValueRow(title: "Size", value: sizeText(d), divider: false)
            }
        }
        .padding(16)
    }

    private func sizeText(_ d: MessageDetail) -> String {
        let n = d.attachments.filter { !($0.inline && $0.contentId != nil) }.count
        return [MessageFormat.size(d.details.sizeBytes), n > 0 ? "\(n) attachment\(n == 1 ? "" : "s")" : nil].compactMap { $0 }.joined(separator: " · ")
    }
}

/// MS08: headers (monospace), SPF/DKIM/DMARC, raw source.
public struct OriginalScreen: View {
    @Environment(\.palette) private var p
    @State private var model: MessageDetailsModel
    public init(env: AppEnvironment, id: UUID) { _model = State(initialValue: MessageDetailsModel(env: env, id: id)) }

    public var body: some View {
        ScrollView {
            if let raw = model.raw { content(raw) }
            else if let e = model.error {
                EmptyState(title: "Can't load the original", message: e, actionTitle: "Try again") { Task { await model.loadOriginal() } }.padding(.top, 60)
            } else { ProgressView().frame(maxWidth: .infinity).padding(.top, 80) }
        }
        .background(p.ground)
        .inboxPushed(title: "Original", back: "Message")
        .task { await model.loadOriginal() }
    }

    private func content(_ raw: RawMessage) -> some View {
        VStack(alignment: .leading, spacing: 16) {
            section("Headers") {
                ForEach(Array(raw.headers.enumerated()), id: \.offset) { _, h in
                    (Text(h.name + ": ").foregroundStyle(p.secondary) + Text(h.value).foregroundStyle(p.text))
                }
            }
            if let a = model.auth {
                section("Authentication") {
                    line("SPF", a.spf); line("DKIM", a.dkim); line("DMARC", a.dmarc)
                }
            }
            section("Raw source") { Text(raw.source).foregroundStyle(p.text) }
        }
        .font(.inboxMono(12)).padding(16).frame(maxWidth: .infinity, alignment: .leading).textSelection(.enabled)
    }

    private func line(_ k: String, _ v: String) -> some View {
        Text(k + ": ").foregroundStyle(p.secondary) + Text(v).foregroundStyle(v.hasPrefix("pass") ? p.text : p.accent)
    }

    private func section<C: View>(_ title: String, @ViewBuilder _ c: () -> C) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title.uppercased()).font(.inboxMono(12)).foregroundStyle(p.secondary)
            c()
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}
