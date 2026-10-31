import SwiftUI
import API
import DesignSystem
import Domain

/// Settings → Gmail: GM01 connected, GM03 signed out, GM04 catching up, GM09 rate-limited, GM05 disconnect alert.
public struct GmailScreen: View {
    @Environment(\.palette) private var p
    @Environment(\.dynamicTypeSize) private var typeSize
    @State private var model: GmailModel
    @State private var applyLabels = true
    @State private var applyArchive = true
    private let back: String
    private let previewDisconnect: Bool

    /// `previewDisconnect` opens the GM05 alert for snapshots.
    public init(env: AppEnvironment, back: String = "Settings", authenticator: any WebAuthenticating = SystemWebAuthenticator(), previewDisconnect: Bool = false) {
        _model = State(initialValue: GmailModel(env: env, authenticator: authenticator)); self.back = back; self.previewDisconnect = previewDisconnect
    }

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                if let m = model.message { Banner(.error, title: m) }
                switch model.store.status?.state {
                case nil: if model.store.failedToLoad { Banner(.warning, title: "Couldn't load Gmail status", actionTitle: "Retry") { Task { await model.load() } } }
                          else { ProgressView().frame(maxWidth: .infinity).padding(.top, 40) }
                case .notConnected?: notConnected
                case .needsReauth?: signedOut
                case .connected?: connected
                }
            }
            .padding(16)
        }
        .background(p.ground)
        .safeAreaInset(edge: .bottom, spacing: 0) { pinned }
        .inboxPushed(title: "Gmail", back: back)
        .task { await model.load(); if previewDisconnect { model.confirmingDisconnect = true } }
        .alert("Disconnect Gmail?", isPresented: $model.confirmingDisconnect) {
            Button("Disconnect", role: .destructive) { Task { await model.disconnect() } }
            Button("Cancel", role: .cancel) {}
        } message: { Text("Labels and archive stop. Mail keeps arriving in Gmail and in Inbox.") }
    }

    /// Primary action pinned above the tab bar: Continue with Google (not connected) / Reconnect Gmail (signed out).
    @ViewBuilder private var pinned: some View {
        switch model.store.status?.state {
        case .notConnected?:
            PrimaryButton(model.isWorking ? "Connecting…" : "Continue with Google") { Task { await model.connect() } }
                .disabled(model.isWorking || !status.configured).opacity(status.configured ? 1 : 0.5).padding(16)
        case .needsReauth?:
            PrimaryButton(model.isWorking ? "Reconnecting…" : "Reconnect Gmail") { Task { await model.connect() } }.disabled(model.isWorking).padding(16)
        default: EmptyView()
        }
    }

    private var status: GmailStatus { model.store.status ?? GmailStatus(state: .notConnected) }

    // MARK: States

    /// Not connected: same pattern as OB05, Continue with Google pinned at the bottom.
    private var notConnected: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Connect Gmail").font(.inboxHeadingBold(27, relativeTo: .title2)).tracking(-0.5).foregroundStyle(p.text)
            Text("Inbox never sends mail. It needs Gmail only to keep it tidy.").font(.inboxBody(14.5)).foregroundStyle(p.secondary)
            if !status.configured { Banner(.warning, title: "Gmail isn't set up on your server yet", message: "Add the Google client in the server settings first.", systemImage: "exclamationmark.triangle") }
            list("Inbox will", ["Label mail by business and rule", "Archive mail your rules archive", "Import older mail once"])
            list("Inbox won't", ["Send, delete or forward anything", "Share your mail with anyone"])
        }
    }

    private func list(_ title: String, _ items: [String]) -> some View {
        InsetGroup(title) {
            ForEach(Array(items.enumerated()), id: \.offset) { i, t in
                InsetRow(t, showsDivider: i < items.count - 1)
            }
        }
    }

    private var signedOut: some View {
        VStack(alignment: .leading, spacing: 18) {
            Banner(.warning, title: "Google signed Inbox out.",
                   message: "\(status.waiting) label and archive action\(status.waiting == 1 ? " is" : "s are") waiting and will run once you reconnect.",
                   systemImage: "exclamationmark.triangle", inline: true)
            InsetGroup { account(subline: "Disconnected", dot: .warning) }
            InsetGroup { disconnectRow }
        }
    }

    private var connected: some View {
        VStack(alignment: .leading, spacing: 18) {
            let limited = status.rateLimitedUntil.map { $0 > AppClock.now } ?? false
            InsetGroup { account(subline: limited ? "Connected · Paused" : "Connected · Read, label and archive", dot: limited ? .warning : .ok) }
            if limited {
                Banner(.warning, title: "Gmail is rate-limiting Inbox.",
                       message: "Labels and archive are paused for a few minutes and will resume on their own.", systemImage: "clock", inline: true)
            }
            if model.store.isCatchingUp {
                VStack(alignment: .leading, spacing: 10) {
                    HStack {
                        Text("Catching up").font(.inboxBody(15, medium: true)).foregroundStyle(p.text)
                        Spacer()
                        Text("\(model.store.done) of \(model.store.peakWaiting)").font(.inboxBody(15)).foregroundStyle(p.secondary)
                    }
                    InboxProgressBar(value: model.store.peakWaiting == 0 ? 0 : Double(model.store.done) / Double(model.store.peakWaiting))
                }
                .padding(.horizontal, 14).padding(.vertical, 12)
                .background(RoundedRectangle(cornerRadius: 12, style: .continuous).fill(p.card))
                .accessibilityElement(children: .combine)
            }
            InsetGroup("Sync") {
                row("Last write", value: status.lastWriteAt.map { $0.formatted(date: .omitted, time: .shortened) } ?? "Never")
                row("Waiting", value: "\(status.waiting)", divider: false)
            }
            InsetGroup {
                InsetRow("Apply labels", showsDivider: true) { SquareSwitch(isOn: $applyLabels).accessibilityLabel("Apply labels") }
                InsetRow("Archive when rules say so", showsDivider: false) { SquareSwitch(isOn: $applyArchive).accessibilityLabel("Archive when rules say so") }
            }
            InsetGroup { disconnectRow }
            Text("Disconnecting stops labels and archive. Mail still arrives in Gmail and in Inbox.")
                .font(.inboxBody(14)).foregroundStyle(p.secondary).padding(.horizontal, 4)
        }
    }

    private func account(subline: String, dot: StatusKind) -> some View {
        HStack(spacing: 12) {
            // DD-18.4: at AX1+ the envelope goes and the address may shrink to 0.75 before it is cut in the middle.
            if !typeSize.isAccessibilitySize { Image(systemName: "envelope").font(.system(size: 22)).foregroundStyle(p.accent).frame(width: 26) }
            VStack(alignment: .leading, spacing: 3) {
                Text(status.email ?? "Gmail").font(.inboxBody(16)).foregroundStyle(p.text).lineLimit(1).minimumScaleFactor(typeSize.isAccessibilitySize ? 0.75 : 1).truncationMode(.middle)
                HStack(spacing: 6) { StatusDot(dot); Text(subline).font(.inboxBody(14)).foregroundStyle(p.secondary) }
            }
            Spacer()
        }
        .padding(.horizontal, 16).padding(.vertical, 10).frame(minHeight: 64)
        .accessibilityElement(children: .combine)
    }

    private var disconnectRow: some View {
        Button { model.confirmingDisconnect = true } label: {
            Text("Disconnect Gmail").font(.inboxBody(16.5)).foregroundStyle(p.accentText).frame(maxWidth: .infinity, minHeight: 56).contentShape(Rectangle())
        }.buttonStyle(.plain)
    }

    private func row(_ title: String, value: String, warn: Bool = false, divider: Bool = true) -> some View {
        InsetRow(title, showsDivider: divider) {
            HStack(spacing: 6) {
                if warn { StatusDot(.warning) }
                Text(value).font(.inboxBody(16)).foregroundStyle(p.secondary).multilineTextAlignment(typeSize.isAccessibilitySize ? .leading : .trailing)
            }
        }
    }
}

/// GM02: quiet banner on Mail when Google signed Inbox out. Reconnect runs the OAuth flow right here.
public struct GmailSignedOutBanner: View {
    @State private var model: GmailModel
    private let env: AppEnvironment
    public init(env: AppEnvironment, authenticator: any WebAuthenticating = SystemWebAuthenticator()) {
        self.env = env; _model = State(initialValue: GmailModel(env: env, authenticator: authenticator))
    }
    public var body: some View {
        if env.gmail.needsReconnect {
            Banner(.warning, title: "Gmail disconnected.", message: "Mail still arrives here; labels and archive in Gmail are paused.",
                   actionTitle: "Reconnect", systemImage: "exclamationmark.triangle", inline: true) { Task { await model.connect() } }
        }
    }
}
