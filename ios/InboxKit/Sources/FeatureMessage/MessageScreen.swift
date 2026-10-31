import SwiftUI
import QuickLook
import SafariServices
import DesignSystem
import Domain
import API
import Web

struct LinkTarget: Identifiable { let url: URL; var id: URL { url } }
struct PreviewTarget: Identifiable { let url: URL; var id: URL { url } }

/// MS01 / MS03 / MS04: header, subject, body, attachments, bottom toolbar; threads collapse earlier messages.
public struct MessageScreen: View {
    @Environment(\.palette) private var p
    @Environment(\.dismiss) private var dismiss
    @State private var model: MessageModel
    @State private var link: LinkTarget?
    @State private var preview: PreviewTarget?
    @State private var showMore = false
    let onNavigate: (Route) -> Void

    public init(env: AppEnvironment, id: UUID, onNavigate: @escaping (Route) -> Void) {
        _model = State(initialValue: MessageModel(env: env, id: id)); self.onNavigate = onNavigate
    }

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                switch model.phase {
                case .loading: ProgressView().frame(maxWidth: .infinity).padding(.top, 80)
                case .failed(let why):
                    EmptyState(title: "Can't open this message", message: why, actionTitle: "Try again") { Task { await model.load() } }.padding(.top, 60)
                case .loaded: loaded
                }
            }
        }
        .background(p.ground)
        .inboxNavigationChrome()
        .toolbar {
            InboxBackButton(model.detail?.details.business?.name ?? "Back") { dismiss() }
            ToolbarItemGroup(placement: .topBarTrailing) {
                Image(systemName: "chevron.up").foregroundStyle(p.accent).opacity(0.35).accessibilityLabel("Previous message").accessibilityHint("Not available")
                Image(systemName: "chevron.down").foregroundStyle(p.accent).opacity(0.35).accessibilityLabel("Next message").accessibilityHint("Not available")
            }.inboxFlat()
        }
        .safeAreaInset(edge: .bottom, spacing: 0) { bottom }
        .sheet(item: $link) { SafariSheet(url: $0.url).ignoresSafeArea() }
        .sheet(item: $preview) { QuickLookSheet(url: $0.url).ignoresSafeArea() }
        .sheet(isPresented: $showMore) {
            MoreSheet(model: model, close: { showMore = false }, onNavigate: { r in showMore = false; onNavigate(r) })
                .presentationDetents([.medium]).presentationDragIndicator(.hidden)
        }
        .task { await model.load() }
        .task(id: model.notice) {
            guard model.notice != nil else { return }
            try? await Task.sleep(for: .seconds(3)); model.notice = nil
        }
    }

    @ViewBuilder private var loaded: some View {
        let thread = model.thread
        if model.isOffline {
            Banner(.idle, title: "Offline", message: "Showing the copy saved on this phone.", systemImage: "icloud.slash")
                .padding(.horizontal, 16).padding(.top, 8)
        }
        if model.gmailState == "not_found" {
            Banner(.warning, title: "Not found in Gmail", message: "Labels and archive wait until Gmail has it.", actionTitle: "Details") {
                onNavigate(.details(model.id))
            }
            .padding(.horizontal, 16).padding(.top, 8)
        }
        if thread.count > 1 {
            HStack(alignment: .firstTextBaseline) {
                Text(thread.last?.subject ?? "(no subject)").font(.inboxHeading(21, relativeTo: .title3)).tracking(-0.2).foregroundStyle(p.text)
                Spacer(minLength: 8)
                Text("\(thread.count)").font(.inboxBody(15)).foregroundStyle(p.secondary)
            }
            .padding(.horizontal, 20).padding(.top, 12).padding(.bottom, 8)
            .accessibilityElement(children: .combine).accessibilityAddTraits(.isHeader)
        }
        ForEach(thread) { m in
            MessageCard(model: model, message: m, showSubject: thread.count == 1, link: { link = LinkTarget(url: $0) },
                        open: { a in Task { if let u = await model.downloadAttachment(a) { preview = PreviewTarget(url: u) } } })
        }
    }

    private var bottom: some View {
        VStack(spacing: 8) {
            if let n = model.notice {
                Toast(n, undoTitle: nil).padding(.horizontal, 20).toastMotion(model.notice)
            }
            HStack {
                tool("Archive", "archivebox") { Task { await model.archive(); dismiss() } }
                Spacer()
                tool("Mark unread", "circle") { Task { await model.markUnread() } }
                Spacer()
                tool("Open in Gmail", "arrow.up.right.square") { Task { await openGmail() } }
                Spacer()
                tool("More", "ellipsis") { showMore = true }
            }
            .padding(.horizontal, 34).frame(height: 50)
            .background(p.ground.ignoresSafeArea(edges: .bottom))
            .overlay(alignment: .top) { Rectangle().fill(p.hairline).frame(height: 1) }
        }
    }

    private func tool(_ title: String, _ icon: String, _ action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: icon).font(.system(size: 22, weight: .regular)).foregroundStyle(p.accent)
                .frame(width: 44, height: 44).contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(title)
        .disabled(model.phase != .loaded)
    }

    @MainActor private func openGmail() async {
        let web = GmailLinks.web(gmailID: model.detail?.details.gmailId)
        if await !UIApplication.shared.open(GmailLinks.app) { await UIApplication.shared.open(web) }
    }
}

/// One message in the thread: collapsed to a line, or open with header, body and attachments.
struct MessageCard: View {
    @Environment(\.palette) private var p
    @Environment(\.dynamicTypeSize) private var typeSize
    let model: MessageModel
    let message: Message
    let showSubject: Bool
    let link: (URL) -> Void
    let open: (Attachment) -> Void

    private var isOpen: Bool { model.expanded.contains(message.id) }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            if isOpen { expanded } else { collapsed }
        }
        .overlay(alignment: .bottom) { Rectangle().fill(p.hairline).frame(height: 1) }
    }

    private var collapsed: some View {
        Button { Task { await model.toggle(message.id) } } label: {
            VStack(alignment: .leading, spacing: 2) {
                HStack {
                    Text(MessageFormat.senderName(message)).font(.inboxBody(16, medium: true)).foregroundStyle(p.text)
                    Spacer()
                    Text(MessageFormat.day(message.receivedAt)).font(.inboxBody(14, relativeTo: .subheadline)).foregroundStyle(p.secondary)
                }
                Text(message.snippet ?? "").font(.inboxBody(15, relativeTo: .subheadline)).foregroundStyle(p.secondary).lineLimit(1)
            }
            
            .padding(.horizontal, 20).padding(.vertical, 12).frame(maxWidth: .infinity, alignment: .leading).contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityHint("Expands this message")
    }

    @ViewBuilder private var expanded: some View {
        header
        if showSubject {
            Text(message.subject?.isEmpty == false ? message.subject! : "(no subject)")
                .font(.inboxHeading(21, relativeTo: .title3)).tracking(-0.2).lineSpacing(4).foregroundStyle(p.text)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 20).padding(.top, 16).padding(.bottom, 14)
                .accessibilityAddTraits(.isHeader)
        }
        bodyView
        if message.id == model.detail?.message.id, !model.attachments.isEmpty {
            VStack(alignment: .leading, spacing: 8) {
                ForEach(model.attachments) { a in AttachmentChip(attachment: a) { open(a) } }
            }
            .padding(.horizontal, 16).padding(.vertical, 12)
        }
    }

    private var avatar: some View {
        Text(MessageFormat.initials(message)).font(.inboxBody(15, medium: true)).foregroundStyle(p.secondary)
            .frame(width: 40, height: 40).background(Circle().fill(p.grouped)).accessibilityHidden(true)
    }

    private var header: some View {
        let sender = Text(MessageFormat.senderName(message)).font(.inboxBody(16, medium: true)).foregroundStyle(p.text)
        let address = message.fromAddr.map { Text($0).font(.inboxBody(14)).foregroundStyle(p.secondary) }
        let to = model.toAddress(message).map { Text("To: \($0)").font(.inboxBody(14)).foregroundStyle(p.secondary) }
        let time = Text(MessageFormat.clock(message.receivedAt)).font(.inboxBody(14, relativeTo: .subheadline)).foregroundStyle(p.secondary)
        let row = Group {
            if typeSize.isAccessibilitySize {
                // Accessibility sizes: everything stacks at full width; nothing is squeezed beside the avatar.
                // The avatar's initials don't fit at these sizes, so it is dropped; long addresses shorten in the middle.
                VStack(alignment: .leading, spacing: 6) {
                    sender.lineLimit(2)
                    address.lineLimit(1).truncationMode(.middle)
                    to.lineLimit(1).truncationMode(.middle)
                    time
                }
            } else {
                HStack(alignment: .top, spacing: 12) {
                    avatar
                    VStack(alignment: .leading, spacing: 1) { sender; address; to }
                    Spacer(minLength: 8)
                    time
                }
            }
        }
        .padding(.horizontal, 20).padding(.vertical, 14).frame(maxWidth: .infinity, alignment: .leading).contentShape(Rectangle())
        .overlay(alignment: .bottom) { if showSubject { Rectangle().fill(p.hairline).frame(height: 1) } }
        return Group {
            if showSubject { row }   // a disabled Button would dim the header
            else { Button { Task { await model.toggle(message.id) } } label: { row }.buttonStyle(.plain) }
        }
    }

    @ViewBuilder private var bodyView: some View {
        switch model.bodies[message.id] ?? .loading {
        case .loading: ProgressView().frame(maxWidth: .infinity).padding(.vertical, 32)
        case .failed:
            EmptyState(title: "Couldn't load the message", message: "Check your connection.", actionTitle: "Try again") {
                Task { await model.loadBody(message.id) }
            }
        case .ready(let body):
            if model.imagesBlocked(for: message, body: body), body.html != nil {
                HStack(spacing: 10) {
                    Image(systemName: "eye.slash").font(.system(size: 16)).foregroundStyle(p.secondary)
                    Text("Remote images are off for this sender.").font(.inboxBody(15)).foregroundStyle(p.text)
                        .fixedSize(horizontal: false, vertical: true)
                    Spacer(minLength: 4)
                    Button("Load images") { Task { await model.loadImages(for: message) } }
                        .font(.inboxBody(15, medium: true)).foregroundStyle(p.accentText).buttonStyle(.plain).frame(minHeight: 44)
                }
                .padding(.horizontal, 14).padding(.vertical, 4)
                .background(RoundedRectangle(cornerRadius: 12, style: .continuous).fill(p.card))
                .padding(.horizontal, 16).padding(.top, 8).padding(.bottom, 6)
            }
            if let html = body.html, !html.isEmpty {
                HTMLBody(html: html, allowImages: model.imagesAllowed(for: message), resolve: model.cidResolver(for: body), link: link)
            } else if let text = body.text, !text.isEmpty {
                Text(PlainText.attributed(text)).font(.inboxBody(16)).lineSpacing(5).foregroundStyle(p.text).tint(p.accentText)
                    .frame(maxWidth: .infinity, alignment: .leading).padding(.horizontal, 20).padding(.bottom, 16)
                    .environment(\.openURL, OpenURLAction { link($0); return .handled })
                    .textSelection(.enabled)
            } else {
                Text("No content").font(.inboxBody(16)).foregroundStyle(p.secondary).padding(16)
            }
        }
    }
}

/// Senders' HTML stays on white, as sent, even in dark mode.
struct HTMLBody: View {
    let html: String
    let allowImages: Bool
    let resolve: @Sendable (String) async -> CIDResource?
    let link: (URL) -> Void
    @State private var height: CGFloat = 1
    var body: some View {
        MessageWebView(html: html, allowImages: allowImages, height: $height, resolveCID: resolve, onOpenLink: link)
            .frame(height: max(height, 1)).background(Color.white)
    }
}

struct AttachmentChip: View {
    @Environment(\.palette) private var p
    let attachment: Attachment
    let action: () -> Void
    var body: some View {
        Button(action: action) {
            HStack(spacing: 12) {
                Image(systemName: "paperclip").foregroundStyle(p.secondary)
                VStack(alignment: .leading, spacing: 0) {
                    Text(attachment.filename ?? "Attachment").font(.inboxBody(16, medium: true)).foregroundStyle(p.text).lineLimit(1)
                    Text(subtitle).font(.inboxBody(13)).foregroundStyle(p.secondary)
                }
                Spacer(minLength: 8)
            }
            .padding(.horizontal, 14).frame(minHeight: 52)
            .background(RoundedRectangle(cornerRadius: 12, style: .continuous).fill(p.grouped))
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Attachment \(attachment.filename ?? ""), \(subtitle)")
    }
    private var subtitle: String {
        let kind = (attachment.filename as NSString?)?.pathExtension.uppercased() ?? ""
        return [kind.isEmpty ? nil : kind, MessageFormat.size(attachment.sizeBytes)].compactMap { $0 }.joined(separator: " · ")
    }
}

struct SafariSheet: UIViewControllerRepresentable {
    let url: URL
    func makeUIViewController(context: Context) -> SFSafariViewController { SFSafariViewController(url: url) }
    func updateUIViewController(_ vc: SFSafariViewController, context: Context) {}
}

struct QuickLookSheet: UIViewControllerRepresentable {
    let url: URL
    func makeCoordinator() -> Source { Source(url) }
    func makeUIViewController(context: Context) -> QLPreviewController {
        let c = QLPreviewController(); c.dataSource = context.coordinator; return c
    }
    func updateUIViewController(_ vc: QLPreviewController, context: Context) {}
    final class Source: NSObject, QLPreviewControllerDataSource {
        let url: URL
        init(_ u: URL) { url = u }
        func numberOfPreviewItems(in controller: QLPreviewController) -> Int { 1 }
        func previewController(_ controller: QLPreviewController, previewItemAt index: Int) -> any QLPreviewItem { url as NSURL }
    }
}
