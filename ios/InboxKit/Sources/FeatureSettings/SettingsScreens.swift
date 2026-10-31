import SwiftUI
import API
import DesignSystem
import Domain
import FeatureOnboarding

// MARK: Health (ST05 / ST06)

public struct HealthScreen: View {
    @Environment(\.palette) private var p
    @State private var model: HealthModel
    let onOpen: (SettingsRoute) -> Void
    public init(env: AppEnvironment, onOpen: @escaping (SettingsRoute) -> Void) { _model = State(initialValue: HealthModel(env: env)); self.onOpen = onOpen }

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                PageTitle("Health", subtitle: model.report == nil ? nil : "\(model.headline) · \(model.checkedText())")
                VStack(alignment: .leading, spacing: 18) {
                    if model.failed { Banner(.warning, title: "Couldn't check right now", message: "Pull to refresh to try again.", systemImage: "exclamationmark.triangle") }
                    if model.isLoading { ProgressView().frame(maxWidth: .infinity).padding(.top, 40) }
                    InsetGroup("Checks") {
                        ForEach(Array(model.rows.enumerated()), id: \.element.key) { i, c in row(c, divider: i < model.rows.count - 1) }
                    }
                    Text("Health checks the Worker, routing for each business, the Gmail connection, the queue and Alfather.")
                        .font(.inboxBody(13.5)).foregroundStyle(p.secondary).padding(.horizontal, 4)
                }.padding(.horizontal, 16).padding(.bottom, 24)
            }
        }
        .background(p.ground)
        .inboxPushed(title: "", back: "Settings")
        .refreshable { await model.load() }
        .task { await model.load() }
    }

    private func row(_ c: HealthCheck, divider: Bool) -> some View {
        let route = HealthModel.route(for: c.fixRoute)
        let content = HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 2) {
                Text(c.title).font(.inboxBody(16.5)).foregroundStyle(p.text)
                Text(c.message).font(.inboxBody(14)).foregroundStyle(p.secondary)
            }
            Spacer()
            StatusDot(kind(c.state))
            if route != nil { Image(systemName: "chevron.right").font(.system(size: 14, weight: .semibold)).foregroundStyle(p.hairlineStrong) }
        }
        .padding(.horizontal, 16).padding(.vertical, 12).frame(minHeight: 60).contentShape(Rectangle())
        .overlay(alignment: .bottom) { if divider { Rectangle().fill(p.hairline).frame(height: 1).padding(.leading, 16) } }
        return Group {
            if let route { Button { onOpen(route) } label: { content }.buttonStyle(.plain).accessibilityHint("Opens the fix") } else { content }
        }
    }
    private func kind(_ s: HealthCheck.State) -> StatusKind {
        switch s { case .ok: .ok; case .warn: .warning; case .error: .error; case .off: .idle }
    }
}

// MARK: Appearance (ST03)

public struct AppearanceScreen: View {
    @Environment(\.palette) private var p
    let env: AppEnvironment
    @State private var loadImages = false
    public init(env: AppEnvironment) { self.env = env }

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                InsetGroup {
                    ForEach(Array([("Light", "light"), ("Dark", "dark"), ("System", "system")].enumerated()), id: \.offset) { i, o in
                        Button { Task { await env.theme.set(o.1, env.database) } } label: {
                            InsetRow(o.0, showsDivider: i < 2) {
                                if env.theme.raw == o.1 {
                                    ZStack { Circle().fill(p.accentFill); Image(systemName: "checkmark").font(.system(size: 12, weight: .bold)).foregroundStyle(p.onAccent) }
                                        .frame(width: 22, height: 22)
                                } else { Color.clear.frame(width: 22, height: 22) }
                            }
                        }.buttonStyle(.plain).accessibilityAddTraits(env.theme.raw == o.1 ? .isSelected : [])
                    }
                }
                InsetGroup("Reading") {
                    InsetRow("Load remote images", subtitle: "Off by default; per sender when you allow it") {
                        SquareSwitch(isOn: Binding(get: { loadImages }, set: { v in loadImages = v; Task { try? await env.database.setPref(ThemePreference.imagesKey, v ? "1" : "0") } }))
                            .accessibilityLabel("Load remote images")
                    }
                    InsetRow("Preview lines", showsDivider: false) {
                        HStack(spacing: 8) {
                            Button { Task { await env.theme.setPreviewLines(max(1, env.theme.previewLines - 1), env.database) } } label: { Image(systemName: "minus.circle") }
                                .accessibilityLabel("Fewer preview lines")
                            Text("\(env.theme.previewLines)").font(.inboxBody(16)).foregroundStyle(p.secondary).frame(minWidth: 18)
                            Button { Task { await env.theme.setPreviewLines(min(4, env.theme.previewLines + 1), env.database) } } label: { Image(systemName: "plus.circle") }
                                .accessibilityLabel("More preview lines")
                        }.font(.system(size: 22)).foregroundStyle(p.accent).buttonStyle(.plain)
                    }
                }
            }.padding(16)
        }
        .background(p.ground)
        .inboxPushed(title: "Appearance", back: "Settings")
        .task {
            await env.theme.load(env.database)
            loadImages = ((try? await env.database.pref(ThemePreference.imagesKey)) ?? nil) == "1"
        }
    }
}

// MARK: Privacy (ST04) and About

public struct AboutScreen: View {
    @Environment(\.palette) private var p
    let env: AppEnvironment
    let openLicence: () -> Void
    public init(env: AppEnvironment, openLicence: @escaping () -> Void = {}) { self.env = env; self.openLicence = openLicence }
    private var version: String { Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "" }
    private var build: String { Bundle.main.infoDictionary?["CFBundleVersion"] as? String ?? "" }
    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                InsetGroup {
                    InsetRow("Version") { Text(version).font(.inboxBody(16)).foregroundStyle(p.secondary) }
                    InsetRow("Build") { Text(build).font(.inboxBody(16)).foregroundStyle(p.secondary) }
                    InsetRow("Server", showsDivider: false) { Text(env.api.baseURL.host ?? env.api.baseURL.absoluteString).font(.inboxBody(16)).foregroundStyle(p.secondary) }
                }
                InsetGroup {
                    Button(action: openLicence) {
                        InsetRow("Open Runde", subtitle: "SIL Open Font License", showsDivider: false) {
                            Image(systemName: "chevron.right").font(.system(size: 14, weight: .semibold)).foregroundStyle(p.hairlineStrong)
                        }
                    }.buttonStyle(.plain)
                }
                // Open Runde ships under the SIL Open Font License 1.1; the licence text is bundled as OFL.txt.
                Text("Typeface: Open Runde, used under the SIL Open Font License.").font(.inboxBody(13, relativeTo: .footnote)).foregroundStyle(p.secondary)
                    .padding(.horizontal, 4).fixedSize(horizontal: false, vertical: true)
            }.padding(16)
        }
        .background(p.ground)
        .inboxPushed(title: "About", back: "Settings")
    }
}

// MARK: Alfather (AF01–AF04)

public struct AlfatherScreen: View {
    @Environment(\.palette) private var p
    @State private var model: AlfatherModel
    @State private var showHelp = false
    public init(env: AppEnvironment, discovery: any AlfatherDiscovering = BonjourAlfatherDiscovery()) { _model = State(initialValue: AlfatherModel(env: env, discovery: discovery)) }

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                if let e = model.error { Banner(.warning, title: e, systemImage: "exclamationmark.triangle") }
                switch model.state {
                case .notPaired: setup
                case .connected: connected
                case .reconnecting: reconnecting
                case .unreachable: unreachable
                }
            }.padding(16)
        }
        .background(p.ground)
        .inboxPushed(title: "Alfather", back: "Settings")
        .sheet(isPresented: $showHelp) { help.environment(\.palette, p).presentationDetents([.medium]) }
        .task { await model.load() }
    }

    private var setup: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Connect Alfather").font(.inboxHeadingBold(27, relativeTo: .title2)).tracking(-0.5).foregroundStyle(p.text)
            Text("Alfather is your local assistant. Inbox pairs the two once; after that Alfather connects to your Inbox server and reads new mail in the background.")
                .font(.inboxBody(15)).lineSpacing(3).foregroundStyle(p.secondary)
            InsetGroup("Steps") {
                InsetRow("1 · Start Alfather on your Mac")
                InsetRow("2 · Keep this phone on the same Wi-Fi for now")
                InsetRow("3 · Tap Look for Alfather", showsDivider: false)
            }
            Text("After pairing, your phone doesn't need to be nearby.").font(.inboxBody(13.5)).foregroundStyle(p.secondary)
            PrimaryButton(model.pairing ? "Pairing with Alfather…" : "Look for Alfather") { Task { await model.pairNow() } }.disabled(model.pairing)
        }
    }

    private var connected: some View {
        VStack(alignment: .leading, spacing: 16) {
            header("Connected", "Connected to your Inbox server · \(model.lastSeenText)")
            InsetGroup("Background reading") {
                InsetRow("Read new mail", showsDivider: true) { Text("On").font(.inboxBody(16)).foregroundStyle(p.secondary) }
                InsetRow("Waiting to be read", showsDivider: false) { Text("\(model.status.waiting)").font(.inboxBody(16)).foregroundStyle(p.secondary) }
            }
            Button { Task { await model.unpair() } } label: {
                Text("Unpair Alfather").font(.inboxBody(16.5)).foregroundStyle(p.accentText).frame(maxWidth: .infinity, minHeight: 52).contentShape(Rectangle())
            }.buttonStyle(.plain)
            note
        }
    }
    private var reconnecting: some View {
        VStack(alignment: .leading, spacing: 16) {
            header("Reconnecting…", "Alfather will pick up where it left off.")
            note
        }
    }
    private var unreachable: some View {
        VStack(alignment: .leading, spacing: 16) {
            header("Not reachable", model.status.lastSeen.map { "Last seen \(RelativeDay.string($0))" } ?? "Not seen yet")
            InsetGroup("Background reading") {
                InsetRow("Read new mail", showsDivider: true) { Text("Paused").font(.inboxBody(16)).foregroundStyle(p.secondary) }
                InsetRow("Waiting to be read", showsDivider: false) { Text("\(model.status.waiting)").font(.inboxBody(16)).foregroundStyle(p.secondary) }
            }
            Text("Mail is unaffected. Alfather reads it when it reconnects; nothing is lost.").font(.inboxBody(14)).foregroundStyle(p.secondary)
            PrimaryButton("Try again") { Task { await model.load() } }
            PrimaryButton("How to connect", style: .plain) { showHelp = true }
            note
        }
    }
    private func header(_ title: String, _ sub: String) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(title).font(.inboxHeadingBold(27, relativeTo: .title2)).tracking(-0.5).foregroundStyle(p.text)
            Text(sub).font(.inboxBody(15)).foregroundStyle(p.secondary)
        }
    }
    private var note: some View {
        Text("In this version nothing from Alfather appears in your mail. It reads in the background so it's ready when that ships.")
            .font(.inboxBody(13.5)).foregroundStyle(p.secondary)
    }
    private var help: some View {
        VStack(alignment: .leading, spacing: 16) {
            SheetHeader("How to connect", trailingTitle: "Close") { showHelp = false }
            Text("Make sure Alfather is running on your Mac and signed in to your Inbox server. If it still doesn't connect, pair again from this phone on the same Wi-Fi.")
                .font(.inboxBody(15)).foregroundStyle(p.secondary).padding(.horizontal, 16)
            PrimaryButton("Pair again") { showHelp = false; Task { await model.pairNow() } }.padding(.horizontal, 16)
            Spacer()
        }
        .background(p.ground)
    }
}
