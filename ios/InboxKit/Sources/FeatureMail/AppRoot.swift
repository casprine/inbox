import SwiftUI
import API
import DesignSystem
import Domain
import Persistence
import FeatureAttention
import FeatureGmail
import FeatureMessage
import FeatureOnboarding
import FeatureRules
import FeatureSettings
import FeatureSearch

/// Root: three-tab shell with the custom `InboxTabBar`. Rules and Settings are placeholders until their tasks.
public struct AppRoot: View {
    let env: AppEnvironment
    let useMockData: Bool

    public init(environment: AppEnvironment, useMockData: Bool = false) { env = environment; self.useMockData = useMockData }

    public var body: some View { ThemedRoot(env: env, useMockData: useMockData) }
}

/// Applies the Appearance choice (light / dark / system) around the whole app.
private struct ThemedRoot: View {
    let env: AppEnvironment
    let useMockData: Bool
    var body: some View {
        OnboardingGate(env: env, useMockData: useMockData)
            .inboxTheme(ThemeMode(rawValue: env.theme.raw) ?? .system)
            .environment(\.mailPreviewLines, env.theme.previewLines)
            .task {
                await env.theme.load(env.database)
                env.theme.onPreviewLinesChanged = { n in Task { _ = try? await env.api.updatePrefs(PrefsPatch(previewLines: n)) } }
                if !useMockData { await PrefsSync.pull(env) }
            }
    }
}

/// Shows onboarding until `onboarding.step == done` (and a device token exists), then the tab shell.
/// In mock mode onboarding only appears with `-Onboarding 1`.
private struct OnboardingGate: View {
    @Environment(\.palette) private var p
    enum State { case checking, onboarding, app }
    let env: AppEnvironment
    let useMockData: Bool
    @SwiftUI.State private var state: State = .checking

    var body: some View {
        Group {
            switch state {
            case .checking: p.ground
            case .onboarding:
                if useMockData {
                    OnboardingFlow(env: env, authenticator: MockWebAuthenticator(), defaults: UserDefaults(suiteName: "inbox.mock.\(UUID().uuidString)") ?? .standard, discovery: MockAlfatherDiscovery()) { state = .app }
                } else { OnboardingFlow(env: env) { state = .app } }
            case .app: AppShell(env: env, useMockData: useMockData)
            }
        }
        .task {
            if useMockData { state = UserDefaults.standard.bool(forKey: "Onboarding") ? .onboarding : .app; return }
            // Dev server (docs/DEV_SERVER.md): a fixed token replaces the password sign-in. Nil in store builds.
            #if DEBUG
            // `-ResetSignIn 1` (UI tests): forget the device token and the onboarding step so the sign-in screen shows.
            if UserDefaults.standard.bool(forKey: "ResetSignIn") {
                await env.tokenStore.clear(); try? await env.database.setPref(OnboardingState.key, OnboardingStep.welcome.rawValue)
            }
            #endif
            let dev = DevConfig.token
            state = await OnboardingState.needsOnboarding(env: env, devToken: dev) ? .onboarding : .app
        }
    }
}

private struct AppShell: View {
    @Environment(\.palette) private var p
    let env: AppEnvironment
    let useMockData: Bool
    @State private var tab: InboxTab = .mail
    @State private var live: LiveEvents
    @State private var path: [Route] = []
    @State private var ready: Bool
    @State private var settingsLink: SettingsRoute?
    @State private var chrome = ChromeState()

    init(env: AppEnvironment, useMockData: Bool) {
        self.env = env; self.useMockData = useMockData
        let l = LiveEvents(); l.refreshEnabled = !useMockData
        _live = State(initialValue: l)
        _ready = State(initialValue: !useMockData)
    }

    /// Full-screen screens own the bottom edge: a message (bottom bar) and Search, whose keyboard would otherwise lift the tab bar
    /// and squash the results above it.
    private func hidesTabBar(for route: Route?) -> Bool {
        switch route { case .message, .search: true; default: false }
    }

    var body: some View {
        VStack(spacing: 0) {
            Group {
                switch tab {
                case .mail where !ready: Color.clear
                case .mail: MailStack(env: env, live: live, path: $path, useMockData: useMockData) { tab = .settings; settingsLink = .importOlder }
                case .rules where !ready: Color.clear
                case .rules: RulesScreen(env: env, live: live.refreshEnabled ? live.tick : 0) { ImportFiltersScreen(env: env) }
                case .settings: SettingsStack(env: env, deepLink: $settingsLink)
                }
            }
            .frame(maxHeight: .infinity)
            if !(tab == .mail && hidesTabBar(for: path.last)) && !chrome.hidesTabBar { InboxTabBar(selection: $tab) }
        }
        .background(p.ground)
        .environment(chrome)
        .task {
            if useMockData {
                try? await MockData.seed(env.database, includeReader: true); ready = true
                // Screenshot aid: `-OpenMessage <n>` launches straight into a mock message.
                let n = UserDefaults.standard.integer(forKey: "OpenMessage")
                if n > 0 { path = [.message(MockData.messageID(n))] }
                // `-OpenSearch` / `-OpenSearchQuery <q>` / `-OpenSearchScope <n>` (1 = Acme) launch into Search.
                let d = UserDefaults.standard
                // `-OpenTab rules` / `-OpenUnrouted` / `-OpenFailed` for screenshots.
                if d.string(forKey: "OpenTab") == "rules" { tab = .rules }
                if d.string(forKey: "OpenTab") == "settings" { tab = .settings }
                if d.bool(forKey: "OpenBusiness") { path = [.business(MockData.acme, address: nil)] }
                if d.bool(forKey: "OpenUnrouted") { path = [.unrouted] }
                if d.bool(forKey: "OpenFailed") { path = [.failed] }
                if d.bool(forKey: "OpenFailedDetail") { path = [.failed, .failedDetail(MockRules.failedID)] }
                if d.bool(forKey: "OpenSearch") || d.string(forKey: "OpenSearchQuery") != nil {
                    if ((try? await env.database.recentSearches()) ?? []).isEmpty {
                        try? await env.database.replaceRecentSearches(["northgate", "invoice october", "from:stripe"])
                    }
                    path = [.search(scope: d.integer(forKey: "OpenSearchScope") == 1 ? MockData.acme : nil)]
                }
            } else { await env.outbox.start() }
        }
        .task { if !useMockData { await live.run(env.sse, onGmail: { env.gmail.apply($0) }, onJob: { env.backfill.apply($0) }) } }
        .task { await env.gmail.refresh(env.api); await env.backfill.refresh(env.api) }
        .task {
            // Retention: the server keeps mail for `retention_days`; saved message copies older than that go too.
            if let raw = (try? await env.database.pref(PrefsSync.retentionKey)) ?? nil, let days = Int(raw), days > 0 {
                _ = try? await env.database.cachePurge(fetchedBefore: Date.now.addingTimeInterval(-Double(days) * 86_400))
            }
        }
        .onOpenURL { url in
            // inbox://gmail/connected?status=ok — the OAuth handshake finished outside ASWebAuthenticationSession.
            if GmailConnectResult.parse(url) != nil { tab = .settings; Task { await env.gmail.refresh(env.api) } }
            else if url.scheme == "inbox", url.host == "settings", url.path == "/health" { tab = .settings; settingsLink = .health }
        }
    }
}

/// Mail tab: a `NavigationStack` over the typed `Route`. Cases owned by later tasks show a placeholder.
public struct MailStack: View {
    let env: AppEnvironment
    let live: LiveEvents
    @Binding var path: [Route]
    let useMockData: Bool
    let openImport: () -> Void

    public init(env: AppEnvironment, live: LiveEvents, path: Binding<[Route]>, useMockData: Bool = false, openImport: @escaping () -> Void = {}) {
        self.env = env; self.live = live; _path = path; self.useMockData = useMockData; self.openImport = openImport
    }

    public var body: some View {
        NavigationStack(path: $path) {
            BusinessesView(env: env, live: live, path: $path, openImport: openImport)
                .navigationDestination(for: Route.self) { route in
                    switch route {
                    case .allBusinesses: AllBusinessesView(env: env, live: live, path: $path)
                    case .business(let id, let address): BusinessPageView(env: env, businessID: id, address: address, live: live, path: $path)
                    case .attention: UnroutedScreen(env: env)
                    case .unrouted: UnroutedScreen(env: env)
                    case .failed: FailedScreen(env: env, path: $path)
                    case .failedDetail(let id): FailedDetailScreen(env: env, id: id)
                    case .message(let id): MessageScreen(env: env, id: id) { path.append($0) }.id(id)
                    case .details(let id): DetailsScreen(env: env, id: id)
                    case .original(let id): OriginalScreen(env: env, id: id)
                    case .search(let scope):
                        SearchScreen(env: env, scope: scope, importing: env.backfill.isActive, useMockData: useMockData,
                                     initialQuery: useMockData ? UserDefaults.standard.string(forKey: "OpenSearchQuery") : nil, path: $path)
                    }
                }
        }
    }
}

struct PlaceholderScreen: View {
    @Environment(\.palette) private var p
    let title: String
    let message: String
    var body: some View {
        VStack { Spacer(); EmptyState(title: title, message: message); Spacer() }
            .frame(maxWidth: .infinity).background(p.ground)
            .navigationTitle(title).navigationBarTitleDisplayMode(.inline)
    }
}
