import SwiftUI
import API
import DesignSystem
import Domain
import FeatureBusinesses
import FeatureGmail

public enum SettingsRoute: Hashable, Sendable {
    case gmail, businesses, importOlder, alfather, health, appearance, privacy, about, licence
    case business(UUID), routing(UUID)
}

/// Settings tab (ST01). Rows open the screens built in T21 (Gmail), T24 (Businesses, Health, Appearance, Privacy, About, Alfather);
/// Import older mail is T26.
public struct SettingsStack: View {
    @Environment(\.palette) private var p
    let env: AppEnvironment
    @Binding var deepLink: SettingsRoute?
    @State private var path: [SettingsRoute] = []
    @State private var businessCount = 0
    @State private var health = ""
    @State private var healthKind: StatusKind = .idle
    @State private var alfatherKind: StatusKind = .idle
    @State private var addingBusiness = false
    @State private var aidApplied = false

    public init(env: AppEnvironment, deepLink: Binding<SettingsRoute?> = .constant(nil)) { self.env = env; _deepLink = deepLink }

    public var body: some View {
        NavigationStack(path: $path) {
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    PageTitle("Settings")
                    VStack(alignment: .leading, spacing: 18) {
                        InsetGroup { row("Gmail", icon: "envelope", value: gmailValue, route: .gmail, divider: false) }
                        InsetGroup {
                            row("Businesses", icon: "building.2", value: businessCount > 0 ? "\(businessCount)" : "", route: .businesses)
                            row("Import older mail", icon: "clock.arrow.circlepath", value: importValue, route: .importOlder, divider: false)
                        }
                        InsetGroup {
                            row("Alfather", icon: "sparkles", dot: alfatherKind, route: .alfather)
                            row("Health", icon: "waveform.path.ecg", value: health, route: .health, divider: false)
                        }
                        InsetGroup {
                            row("Appearance", icon: "circle.lefthalf.filled", value: appearanceValue, route: .appearance)
                            row("Privacy", icon: "lock", route: .privacy)
                            row("About", icon: "info.circle", value: appVersion, route: .about, divider: false)
                        }
                    }
                    .padding(.horizontal, 16).padding(.bottom, 24)
                }
            }
            .background(p.ground)
            .inboxNavigationChrome()
            .navigationDestination(for: SettingsRoute.self) { route in destination(route) }
            .task { await refresh() }
            .onChange(of: deepLink) { if let d = deepLink { path = [d]; deepLink = nil } }
        }
        .sheet(isPresented: $addingBusiness, onDismiss: { Task { await refresh() } }) {
            AddBusinessSheet(env: env).environment(\.palette, p).presentationDetents([.large])
        }
    }

    @ViewBuilder private func destination(_ r: SettingsRoute) -> some View {
        switch r {
        case .gmail: GmailScreen(env: env)
        case .businesses:
            BusinessesSettingsScreen(env: env, onOpen: { path.append(.business($0)) }, onAdd: { addingBusiness = true })
        case .business(let id):
            BusinessDetailScreen(env: env, id: id, onRouting: { path.append(.routing(id)) }, onRemoved: { path.removeAll { if case .business = $0 { true } else { false } } })
        case .routing(let id): RoutingProblemScreen(env: env, id: id)
        case .health: HealthScreen(env: env) { path.append($0) }
        case .appearance: AppearanceScreen(env: env)
        case .privacy: PrivacyScreen(env: env)
        case .about: AboutScreen(env: env) { path.append(.licence) }
        case .licence: LicenceScreen()
        case .alfather: AlfatherScreen(env: env)
        case .importOlder: ImportOlderScreen(env: env)
        }
    }

    private func refresh() async {
        businessCount = ((try? await env.database.businesses()) ?? []).count
        await env.theme.load(env.database)
        await env.gmail.refresh(env.api)
        await env.backfill.refresh(env.api)
        let h = HealthModel(env: env)
        await h.load()
        health = await h.rowValue
        healthKind = h.report == nil ? .idle : (h.report!.problems.isEmpty ? .ok : .warning)
        if let s = try? await env.api.alfatherStatus() {
            alfatherKind = switch s.state(now: AppClock.now) { case .connected: .ok; case .reconnecting: .warning; case .unreachable: .error; case .notPaired: .idle }
        }
        // Screenshot aids (mock mode): `-OpenGmail 1`, `-OpenSettings <route>`.
        let d = UserDefaults.standard
        if path.isEmpty, !aidApplied {
            aidApplied = true
            if d.bool(forKey: "OpenGmail") { path = [.gmail] }
            else if let r = d.string(forKey: "OpenSettings") {
                switch r {
                case "businesses": path = [.businesses]
                case "business": if let b = try? await env.database.businesses().first { path = [.businesses, .business(b.id)] }
                case "routing": if let b = try? await env.database.businesses().first(where: { $0.routing != "ok" }) { path = [.businesses, .business(b.id), .routing(b.id)] }
                case "health": path = [.health]
                case "appearance": path = [.appearance]
                case "privacy": path = [.privacy]
                case "alfather": path = [.alfather]
                case "about": path = [.about]
                case "licence": path = [.about, .licence]
                case "import": path = [.importOlder]
                default: break
                }
            }
        }
    }

    private var importValue: String {
        let b = env.backfill
        if b.isActive { return b.isPaused ? "Paused" : "\(Int(b.progress * 100))%" }
        guard b.batchFinished, let d = b.days else { return "" }
        return d == 365 ? "1 year imported" : "\(d) days imported"
    }

    private var appearanceValue: String { env.theme.raw.prefix(1).uppercased() + env.theme.raw.dropFirst() }
    private var gmailValue: String {
        switch env.gmail.status?.state {
        case .connected?: env.gmail.status?.email ?? "Connected"
        case .needsReauth?: "Signed out"
        case .notConnected?: "Not connected"
        case nil: ""
        }
    }
    private var appVersion: String { Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "" }

    private func row(_ title: String, icon: String, value: String = "", dot: StatusKind? = nil, route: SettingsRoute, divider: Bool = true) -> some View {
        Button { path.append(route) } label: {
            InsetRow(title, showsDivider: divider, systemImage: icon) {
                HStack(spacing: 8) {
                    if let dot { StatusDot(dot) }
                    if !value.isEmpty { Text(value).font(.inboxBody(16)).foregroundStyle(p.secondary) }
                    Image(systemName: "chevron.right").font(.system(size: 14, weight: .semibold)).foregroundStyle(p.hairlineStrong)
                }
            }
        }
        .buttonStyle(.plain)
    }
}

struct ComingSoon: View {
    @Environment(\.palette) private var p
    let title: String
    var body: some View {
        VStack { Spacer(); EmptyState(title: title, message: "This screen arrives in a later update."); Spacer() }
            .frame(maxWidth: .infinity).background(p.ground).inboxPushed(title: title, back: "Settings")
    }
}
