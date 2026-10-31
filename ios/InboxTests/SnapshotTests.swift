import SwiftUI
import Testing
import API
import Persistence
import Domain
import DesignSystem
import FeatureMail
import FeatureRules
import FeatureGmail
import FeatureSettings
@testable import FeatureOnboarding

/// One snapshot per canvas board that doesn't show clock-dependent content. Board ids in the names (ML01, RU01, …).
@MainActor
private func seeded() async throws -> AppEnvironment {
    let env = try AppEnvironment.preview()
    try await MockData.seed(env.database)
    return env
}

/// The harness window has no status bar or home indicator: pad like a real iPhone (54 pt / 30 pt), fill with the ground colour and
/// ignore the keyboard, so pinned footers are visible and a focused field doesn't shift the picture.
private struct DeviceFrame<Content: View>: View {
    @Environment(\.palette) private var p
    @ViewBuilder let content: Content
    var body: some View { content.padding(.top, 54).padding(.bottom, 30).frame(maxWidth: .infinity, maxHeight: .infinity).background(p.ground).ignoresSafeArea() }
}

/// Sign-in screen (OB01b) in a given state. The clock is fixed so lock-out countdowns render the same every run.
@MainActor
private func signIn(_ env: AppEnvironment, _ mode: SignInMode = .new, address: String = "", password: String = "", revealed: Bool = false,
                    issue: SignInModel.Issue? = nil, loading: Bool = false, lock: TimeInterval? = nil, ring: SignInField? = nil) -> some View {
    let onboarding = OnboardingModel(env: env, authenticator: MockWebAuthenticator(), defaults: scratchDefaults(), discovery: MockAlfatherDiscovery())
    let m = SignInModel(env: env, mode: mode, defaults: scratchDefaults(), deviceName: "Test", now: { Date(timeIntervalSince1970: 1_800_000_000) })
    m.address = address; m.password = password; m.revealed = revealed
    if let lock, let url = URL(string: "https://locked.example.com") { m.address = url.absoluteString; m.lock(url, for: lock) }
    m.show(issue, loading: loading)
    return DeviceFrame { SignInStep(onboarding: onboarding, model: m, autoFocus: false, ringOverride: ring) }.inboxTheme(.system)
}

private struct BusinessesHost: View {
    let env: AppEnvironment
    var editing = false
    @State private var path: [Route] = []
    @State private var live: LiveEvents = { let l = LiveEvents(); l.refreshEnabled = false; return l }()
    var body: some View { NavigationStack { BusinessesView(env: env, live: live, path: $path, startEditing: editing) }.inboxTheme(.system) }
}

@MainActor @Suite(.serialized) struct SnapshotTests {
    @Test func designGallery() async { await Snapshot.check("design-gallery") { DesignGallery(initialMode: .system) } }

    @Test func ML01_businesses() async throws {
        let env = try await seeded()
        await Snapshot.check("ML01-businesses") { BusinessesHost(env: env) }
    }

    @Test func BZ02_businessesEdit() async throws {
        let env = try await seeded()
        await Snapshot.check("BZ02-businesses-edit") { BusinessesHost(env: env, editing: true) }
    }

    @Test func RU01_rules() async throws {
        let env = try await seeded()
        await Snapshot.check("RU01-rules") { RulesScreen(env: env) { EmptyView() }.inboxTheme(.system) }
    }

    @Test func ST01_settings() async throws {
        let env = try await seeded()
        await Snapshot.check("ST01-settings") { SettingsStack(env: env).inboxTheme(.system) }
    }

    /// The mock server answers `/v1/gmail/status` from the `GmailState` default; pin it for the test.
    private func withGmailState<T>(_ state: String, _ body: () async throws -> T) async rethrows -> T {
        let d = UserDefaults.standard
        let before = d.string(forKey: "GmailState")
        d.set(state, forKey: "GmailState")
        defer { if let before { d.set(before, forKey: "GmailState") } else { d.removeObject(forKey: "GmailState") } }
        return try await body()
    }

    @Test func GM03_gmailSignedOut() async throws {
        let env = try await seeded()
        await withGmailState("needs_reauth") {
            await Snapshot.check("GM03-gmail-signed-out") { NavigationStack { GmailScreen(env: env) }.inboxTheme(.system) }
        }
    }

    @Test func OB05b_gmailNotConnected() async throws {
        let env = try await seeded()
        await withGmailState("not_connected") {
            await Snapshot.check("GM00-gmail-not-connected") { NavigationStack { GmailScreen(env: env) }.inboxTheme(.system) }
        }
    }

    @Test func ST04_privacy() async throws {
        let env = try await seeded()
        await Snapshot.check("ST04-privacy") { NavigationStack { PrivacyScreen(env: env) }.inboxTheme(.system) }
    }

    @Test func ST03_appearance() async throws {
        let env = try await seeded()
        await Snapshot.check("ST03-appearance") { NavigationStack { AppearanceScreen(env: env) }.inboxTheme(.system) }
    }

    @Test func RU04_importFilters() async throws {
        let env = try await seeded()
        await Snapshot.check("RU04-import-filters") { NavigationStack { ImportFiltersScreen(env: env) }.inboxTheme(.system) }
    }

    @Test func OB01_welcome() async throws {
        let env = try await seeded()
        let m = OnboardingModel(env: env, authenticator: MockWebAuthenticator(), discovery: MockAlfatherDiscovery())
        await Snapshot.check("OB01-welcome") { DeviceFrame { WelcomeStep(model: m) }.inboxTheme(.system) }
    }

    @Test func OB02_domains() async throws {
        let env = try await seeded()
        let m = OnboardingModel(env: env, authenticator: MockWebAuthenticator(), discovery: MockAlfatherDiscovery())
        m.jump(to: .domains)
        await Snapshot.check("OB02-domains") { DomainsStep(model: m).inboxTheme(.system) }
    }

    @Test func OB05_gmail() async throws {
        let env = try await seeded()
        let m = OnboardingModel(env: env, authenticator: MockWebAuthenticator(), discovery: MockAlfatherDiscovery())
        m.jump(to: .gmail)
        await Snapshot.check("OB05-gmail") { GmailStep(model: m).inboxTheme(.system) }
    }

    @Test func OB09_backfill() async throws {
        let env = try await seeded()
        let m = OnboardingModel(env: env, authenticator: MockWebAuthenticator(), estimator: StubBackfillEstimator(), discovery: MockAlfatherDiscovery())
        m.jump(to: .backfill)
        await Snapshot.check("OB09-backfill") { BackfillStep(model: m).inboxTheme(.system) }
    }

    @Test func OB10_alfather() async throws {
        let env = try await seeded()
        let m = OnboardingModel(env: env, authenticator: MockWebAuthenticator(), discovery: MockAlfatherDiscovery())
        m.jump(to: .alfather)
        await Snapshot.check("OB10-alfather") { AlfatherStep(model: m).inboxTheme(.system) }
    }

    // MARK: Accessibility Dynamic Type (AX3): Mail, Rules, Settings and two forms. Message is checked by hand (web content).

    @Test func AX3_ML01_businesses() async throws {
        let env = try await seeded()
        await Snapshot.check("AX3-ML01-businesses", settle: .milliseconds(2_500), typeSize: .accessibility3) { BusinessesHost(env: env) }
    }
    @Test func AX3_ML01_businessesEdit() async throws {
        let env = try await seeded()
        await Snapshot.check("AX3-BZ02-businesses-edit", settle: .milliseconds(2_500), typeSize: .accessibility3) { BusinessesHost(env: env, editing: true) }
    }
    @Test func AX3_RU01_rules() async throws {
        let env = try await seeded()
        await Snapshot.check("AX3-RU01-rules", settle: .milliseconds(2_500), typeSize: .accessibility3) { RulesScreen(env: env) { EmptyView() }.inboxTheme(.system) }
    }
    @Test func AX3_ST01_settings() async throws {
        let env = try await seeded()
        await Snapshot.check("AX3-ST01-settings", settle: .milliseconds(2_500), typeSize: .accessibility3) { SettingsStack(env: env).inboxTheme(.system) }
    }
    @Test func AX3_GM03_gmailSignedOut() async throws {
        let env = try await seeded()
        await withGmailState("needs_reauth") {
            await Snapshot.check("AX3-GM03-gmail-signed-out", settle: .milliseconds(2_500), typeSize: .accessibility3) { NavigationStack { GmailScreen(env: env) }.inboxTheme(.system) }
        }
    }
    @Test func AX3_OB01_welcome() async throws {
        let env = try await seeded()
        let m = OnboardingModel(env: env, authenticator: MockWebAuthenticator(), discovery: MockAlfatherDiscovery())
        await Snapshot.check("AX3-OB01-welcome", settle: .milliseconds(2_500), typeSize: .accessibility3, height: 1_800) { DeviceFrame { WelcomeStep(model: m) }.inboxTheme(.system) }
    }

    // MARK: OB01b Sign in (D-012)

    @Test func OB01b_emptyNew() async throws {
        let env = try await seeded()
        await Snapshot.check("OB01b-empty-new") { signIn(env, ring: .address) }
    }
    @Test func OB01b_ready() async throws {
        let env = try await seeded()
        await Snapshot.check("OB01b-ready") { signIn(env, address: "https://mini.tail1234.ts.net", password: "correct horse") }
    }
    @Test func OB01b_passwordShown() async throws {
        let env = try await seeded()
        await Snapshot.check("OB01b-password-shown") { signIn(env, address: "https://mini.tail1234.ts.net", password: "correct horse", revealed: true) }
    }
    @Test func OB01b_loading() async throws {
        let env = try await seeded()
        await Snapshot.check("OB01b-loading") { signIn(env, address: "https://mini.tail1234.ts.net", password: "correct horse", loading: true) }
    }
    @Test func OB01b_invalidURL() async throws {
        let env = try await seeded()
        await Snapshot.check("OB01b-invalid-url") { signIn(env, address: "inbox", password: "correct horse", issue: .invalidURL(insecure: false)) }
        await Snapshot.check("OB01b-invalid-url-http") { signIn(env, address: "http://inbox.example.com", password: "correct horse", issue: .invalidURL(insecure: true)) }
    }
    @Test func OB01b_wrongPassword() async throws {
        let env = try await seeded()
        await Snapshot.check("OB01b-wrong-password") { signIn(env, address: "https://mini.tail1234.ts.net", issue: .wrongPassword) }
    }
    @Test func OB01b_notInboxServer() async throws {
        let env = try await seeded()
        await Snapshot.check("OB01b-not-inbox") { signIn(env, address: "https://example.com", password: "correct horse", issue: .notInboxServer) }
    }
    @Test func OB01b_locked() async throws {
        let env = try await seeded()
        await Snapshot.check("OB01b-locked") { signIn(env, password: "correct horse", lock: 272) }
        await Snapshot.check("OB01b-locked-long") { signIn(env, password: "correct horse", lock: 840) }
    }
    @Test func OB01b_unreachable() async throws {
        let env = try await seeded()
        await Snapshot.check("OB01b-unreachable") { signIn(env, address: "https://mini.tail1234.ts.net", password: "correct horse", issue: .unreachable(.network)) }
        await Snapshot.check("OB01b-unreachable-tls") { signIn(env, address: "https://mini.tail1234.ts.net", password: "correct horse", issue: .unreachable(.tls(host: "mini.tail1234.ts.net"))) }
        await Snapshot.check("OB01b-unreachable-5xx") { signIn(env, address: "https://mini.tail1234.ts.net", password: "correct horse", issue: .unreachable(.server)) }
    }
    @Test func OB01b_modes() async throws {
        let env = try await seeded()
        await Snapshot.check("OB01b-existing") { signIn(env, .existing, address: "https://mini.tail1234.ts.net") }
        await Snapshot.check("OB01b-signed-out") { signIn(env, .signedOut, address: "https://mini.tail1234.ts.net") }
    }

    // MARK: Accessibility Dynamic Type (AX3): sign-in

    @Test func AX3_OB01b_empty() async throws {
        let env = try await seeded()
        await Snapshot.check("AX3-OB01b-empty", settle: .milliseconds(2_500), typeSize: .accessibility3, height: 2_400) { signIn(env) }
    }
    @Test func AX3_OB01b_locked() async throws {
        let env = try await seeded()
        await Snapshot.check("AX3-OB01b-locked", settle: .milliseconds(2_500), typeSize: .accessibility3, height: 2_400) { signIn(env, password: "correct horse", lock: 272) }
    }
    @Test func AX3_OB01b_errors() async throws {
        let env = try await seeded()
        await Snapshot.check("AX3-OB01b-wrong-password", settle: .milliseconds(2_500), typeSize: .accessibility3, height: 2_400) { signIn(env, address: "https://mini.tail1234.ts.net", issue: .wrongPassword) }
        await Snapshot.check("AX3-OB01b-unreachable", settle: .milliseconds(2_500), typeSize: .accessibility3, height: 2_400) { signIn(env, address: "https://mini.tail1234.ts.net", password: "x", issue: .unreachable(.network)) }
    }
}
