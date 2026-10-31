import SwiftUI
import Testing
import API
import Persistence
import Domain
import DesignSystem
@testable import FeatureMail
@testable import FeatureMessage
@testable import FeatureSearch
@testable import FeatureRules
@testable import FeatureAttention
@testable import FeatureGmail
@testable import FeatureBusinesses
@testable import FeatureSettings
@testable import FeatureOnboarding

/// Round D3 (H-8): baselines for the screens that had no Open Runde snapshot (GATE11 §6). Board ids in the names.
/// All of them run with a pinned clock (`AppClock.fixed`) so relative times ("Yesterday", "6 days") are the same on every run.
/// Message bodies are real WKWebView renders of the mock HTML (white, as sent); see docs/STATE_IOS.md D3 for what that does and does not prove.

/// Friday 31 Oct 2026, 14:30 UTC.
private let fixedNow = Date(timeIntervalSince1970: 1_793_457_000)

@MainActor
private func withClock<T>(_ body: () async throws -> T) async rethrows -> T {
    AppClock.fixed = fixedNow
    defer { AppClock.fixed = nil }
    return try await body()
}

/// The mock server reads its state from UserDefaults (the same keys as the launch arguments).
@MainActor
private func withDefaults<T>(_ pairs: [String: String], _ body: () async throws -> T) async rethrows -> T {
    let d = UserDefaults.standard
    let before = pairs.keys.map { ($0, d.string(forKey: $0)) }
    for (k, v) in pairs { d.set(v, forKey: k) }
    defer { for (k, old) in before { if let old { d.set(old, forKey: k) } else { d.removeObject(forKey: k) } } }
    return try await body()
}

@MainActor
private func fixedEnv(reader: Bool = false) async throws -> AppEnvironment {
    let env = try AppEnvironment.preview()
    try await MockData.seed(env.database, now: AppClock.now, includeReader: reader)
    return env
}

/// The harness window has no home-indicator inset: lift bottom bars and toasts like a real iPhone (30 pt) so they are not cut off.
private struct BottomFrame<Content: View>: View {
    @Environment(\.palette) private var p
    @ViewBuilder let content: Content
    var body: some View { content.padding(.bottom, 30).frame(maxWidth: .infinity, maxHeight: .infinity).background(p.ground).ignoresSafeArea() }
}

/// A screen with a sheet open from the start (sheets the screens only open on a tap).
private struct SheetHost<Base: View, Sheet: View>: View {
    @Environment(\.palette) private var p
    @State private var open = true
    let base: Base
    let sheet: Sheet
    init(@ViewBuilder base: () -> Base, @ViewBuilder sheet: () -> Sheet) { self.base = base(); self.sheet = sheet() }
    var body: some View { base.sheet(isPresented: $open) { sheet.environment(\.palette, p) } }
}

/// The Mail tab's real NavigationStack, started on a route, so the shot has the real back button and title.
private struct MailRouteHost: View {
    let env: AppEnvironment
    @State private var path: [Route]
    @State private var live: LiveEvents = { let l = LiveEvents(); l.refreshEnabled = false; return l }()
    init(env: AppEnvironment, _ route: [Route]) { self.env = env; _path = State(initialValue: route) }
    var body: some View { BottomFrame { MailStack(env: env, live: live, path: $path, useMockData: true) }.inboxTheme(.system).ignoresSafeArea(.keyboard) }
}

/// MessageListScreen with a model the test has put into a state (select mode, toast, empty).
private struct ListHost: View {
    let model: MessageListModel
    let title: String
    let subtitle: String
    @State private var path: [Route] = []
    @State private var live: LiveEvents = { let l = LiveEvents(); l.refreshEnabled = false; return l }()
    var body: some View {
        BottomFrame { NavigationStack {
            MessageListScreen(model: model, live: live, path: $path, searchScope: nil) {
                VStack(alignment: .leading, spacing: 0) {
                    PageHeader(title: title, subtitle: subtitle)
                    ScrollView(.horizontal, showsIndicators: false) {
                        HStack(spacing: 8) {
                            ForEach(model.chips) { chip in
                                Chip(chip.address == nil ? "All \(chip.count)" : "\(chip.title) \(chip.count)", selected: model.selectedAddress == chip.address) {}
                            }
                        }.padding(.horizontal, 20)
                    }.padding(.bottom, 8)
                }
            }
        } }.inboxTheme(.system)
    }
}

private struct SlowAuthenticator: WebAuthenticating {
    @MainActor func authenticate(url: URL, callbackScheme: String) async throws -> URL {
        try await Task.sleep(for: .seconds(120))
        return URL(string: "inbox://gmail/connected?status=ok")!
    }
}

private func backfill(_ name: String, _ domain: String, status: String, est: Int, scanned: Int, extra: String = "") -> Backfill? {
    let json = #"{"id":"\#(UUID().uuidString.lowercased())","business_id":"\#(UUID().uuidString.lowercased())","business_name":"\#(name)","domain":"\#(domain)","since":"2026-08-01","status":"\#(status)","estimated":\#(est),"scanned":\#(scanned),"imported":\#(scanned - 2),"sorted":\#(scanned / 2),"unrouted":\#(scanned / 10),"skipped":2,"paused_until":null,"pause_reason":null,"last_error":null,"created_at":"2026-10-31T09:00:00Z","started_at":"2026-10-31T09:00:05Z","finished_at":null\#(extra)}"#
    return try? APIClient.makeDecoder().decode(Backfill.self, from: Data(json.utf8))
}

private let ax3 = DynamicTypeSize.accessibility3
private let slow = Duration.milliseconds(2_500)

@MainActor @Suite(.serialized) struct SnapshotCoverageTests {

    // MARK: Onboarding: OB03, OB04, OB06, OB08, OB11, OB13

    private func onboarding(_ env: AppEnvironment, discovery: any AlfatherDiscovering = MockAlfatherDiscovery(), auth: any WebAuthenticating = MockWebAuthenticator()) -> OnboardingModel {
        OnboardingModel(env: env, authenticator: auth, estimator: StubBackfillEstimator(), discovery: discovery)
    }

    @Test func OB03_fixRouting() async throws {
        try await withClock {
            let env = try await fixedEnv()
            let m = onboarding(env)
            m.jump(to: .domains)
            await m.loadDomains()
            m.fixing = m.domains.first { $0.domain == "kettle.house" }
            await Snapshot.check("OB03-fix-routing") { DeviceFrame { DomainsStep(model: m) }.inboxTheme(.system) }
        }
    }
    @Test func OB04_routingFixed() async throws {
        try await withClock {
            let env = try await fixedEnv()
            let m = onboarding(env)
            m.jump(to: .domains)
            await m.loadDomains()
            if let d = m.domains.first(where: { $0.domain == "kettle.house" }) { await m.checkAgain(d) }
            await Snapshot.check("OB04-routing-fixed") { DeviceFrame { DomainsStep(model: m) }.inboxTheme(.system) }
        }
    }
    @Test func OB06_googleHandoff() async throws {
        try await withClock {
            let env = try await fixedEnv()
            let m = onboarding(env, auth: SlowAuthenticator())
            m.jump(to: .gmail)
            let t = Task { await m.connectGmail() }
            try await Task.sleep(for: .milliseconds(300))
            await Snapshot.check("OB06-google-handoff") { DeviceFrame { GmailStep(model: m) }.inboxTheme(.system) }
            t.cancel()
        }
    }
    @Test func OB08_filters() async throws {
        try await withClock {
            let env = try await fixedEnv()
            let m = onboarding(env)
            m.jump(to: .filters)
            await Snapshot.check("OB08-filters") { DeviceFrame { FiltersStep(model: m, env: env) }.inboxTheme(.system) }
        }
    }
    @Test func OB11_alfatherNotFound() async throws {
        try await withClock {
            let env = try await fixedEnv()
            let m = onboarding(env, discovery: MockAlfatherDiscovery(host: nil))
            m.jump(to: .alfather)
            await Snapshot.check("OB11-alfather-not-found") { DeviceFrame { AlfatherStep(model: m) }.inboxTheme(.system) }
        }
    }
    @Test func AX3_OB11_alfatherNotFound() async throws {
        try await withClock {
            let env = try await fixedEnv()
            let m = onboarding(env, discovery: MockAlfatherDiscovery(host: nil))
            m.jump(to: .alfather)
            await Snapshot.check("AX3-OB11-alfather-not-found", settle: .milliseconds(3_500), typeSize: ax3, height: 2_000) { DeviceFrame { AlfatherStep(model: m) }.inboxTheme(.system) }
        }
    }
    @Test func OB13_firstOpenImportBanner() async throws {
        try await withClock {
            let env = try await fixedEnv()
            env.backfill.set([backfill("Acme Studio", "acme-studio.com", status: "running", est: 1_800, scanned: 640), backfill("Northgate Ltd", "northgate.co", status: "running", est: 1_380, scanned: 600)].compactMap { $0 })
            env.backfill.noteStarted(days: 90)
            await Snapshot.check("OB13-first-open-import") { BusinessesHost(env: env) }
        }
    }
    @Test func AX3_OB13_firstOpenImportBanner() async throws {
        try await withClock {
            let env = try await fixedEnv()
            env.backfill.set([backfill("Acme Studio", "acme-studio.com", status: "running", est: 1_800, scanned: 640)].compactMap { $0 })
            env.backfill.noteStarted(days: 90)
            await Snapshot.check("AX3-OB13-first-open-import", settle: slow, typeSize: ax3, height: 1_800) { BusinessesHost(env: env) }
        }
    }

    // MARK: Mail root banners: GM02 (Gmail disconnected), Needs attention row (NA01)

    @Test func GM02_mailGmailBanner() async throws {
        try await withClock {
            let env = try await fixedEnv()
            env.gmail.set(GmailStatus(state: .needsReauth, email: "sam@gmail.com", waiting: 14))
            await Snapshot.check("GM02-mail-gmail-banner") { BusinessesHost(env: env) }
        }
    }
    @Test func NA01_mailNeedsAttention() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await Snapshot.check("NA01-needs-attention-root") { BusinessesHost(env: env) }
        }
    }
    @Test func AX3_NA01_mailNeedsAttention() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await Snapshot.check("AX3-NA01-needs-attention-root", settle: slow, typeSize: ax3, height: 1_800) { BusinessesHost(env: env) }
        }
    }

    // MARK: Business page / lists: ML02 normal, ML06 archived toast, ML07 select, empty, AX3

    private func businessModel(_ env: AppEnvironment) -> MessageListModel {
        MessageListModel(env: env, scope: .business(MockData.acme), toastDuration: .seconds(600))
    }

    @Test func ML02_businessPageFixedClock() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await Snapshot.check("ML02-business-page-fixed") { BusinessPageHost(env: env, id: MockData.acme) }
        }
    }
    @Test func AX3_ML02_businessPageFixedClock() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await Snapshot.check("AX3-ML02-business-page-fixed", settle: slow, typeSize: ax3, height: 2_600) { BusinessPageHost(env: env, id: MockData.acme) }
        }
    }
    @Test func ML06_archivedToast() async throws {
        try await withClock {
            let env = try await fixedEnv()
            env.gmail.set(GmailStatus(state: .connected, email: "sam@gmail.com"))   // mock has Gmail connected (DD-11)
            let model = businessModel(env)
            await model.loadCache()
            await model.archive([MockData.messageID(3)])
            await Snapshot.check("ML06-archived-toast") { ListHost(model: model, title: "Acme Studio", subtitle: unreadSubtitle("acme-studio.com", 2)) }
        }
    }
    @Test func ML07_select() async throws {
        try await withClock {
            let env = try await fixedEnv()
            let model = businessModel(env)
            await model.loadCache()
            model.beginSelecting(MockData.messageID(1))
            model.toggleSelection(MockData.messageID(2))
            await Snapshot.check("ML07-select") { ListHost(model: model, title: "Acme Studio", subtitle: unreadSubtitle("acme-studio.com", 2)) }
        }
    }
    @Test func AX3_ML07_select() async throws {
        try await withClock {
            let env = try await fixedEnv()
            let model = businessModel(env)
            await model.loadCache()
            model.beginSelecting(MockData.messageID(1))
            await Snapshot.check("AX3-ML07-select", settle: slow, typeSize: ax3, height: 2_600) { ListHost(model: model, title: "Acme Studio", subtitle: unreadSubtitle("acme-studio.com", 2)) }
        }
    }
    /// The "Couldn't refresh" banner above saved rows (offline mock: the refresh fails, the cached rows stay).
    @Test func ML10_refreshFailed() async throws {
        try await withClock {
            let env = try await fixedEnv()
            let model = businessModel(env)
            await model.loadCache()
            await model.refresh()
            await Snapshot.check("ML10-refresh-failed") { ListHost(model: model, title: "Acme Studio", subtitle: unreadSubtitle("acme-studio.com", 2)) }
        }
    }
    @Test func ML08_emptyBusiness() async throws {
        try await withClock {
            let env = try AppEnvironment.preview()
            try await env.database.replaceBusinesses(MockData.businesses(now: AppClock.now))
            let model = MessageListModel(env: env, scope: .business(MockData.acme))
            await model.loadCache()
            await model.refresh()   // offline mock: marks the list as loaded (empty), the refresh error banner only shows with rows
            await Snapshot.check("ML08-empty-business") { ListHost(model: model, title: "Acme Studio", subtitle: unreadSubtitle("acme-studio.com", 0)) }
        }
    }
    @Test func ML09_allBusinesses() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await Snapshot.check("ML09-all-businesses") { MailRouteHost(env: env, [.allBusinesses]) }
        }
    }
    @Test func AX3_ML09_allBusinesses() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await Snapshot.check("AX3-ML09-all-businesses", settle: slow, typeSize: ax3, height: 2_400) { MailRouteHost(env: env, [.allBusinesses]) }
        }
    }

    // MARK: Message: MS01, MS03, MS04, MS05, MS06, MS07, MS08, MS09

    @Test func MS01_message() async throws {
        try await withClock {
            let env = try await fixedEnv(reader: true)
            await Snapshot.check("MS01-message", settle: slow) { MailRouteHost(env: env, [.message(MockContent.invoiceID)]) }
        }
    }
    @Test func AX3_MS01_message() async throws {
        try await withClock {
            let env = try await fixedEnv(reader: true)
            await Snapshot.check("AX3-MS01-message", settle: .milliseconds(3_500), typeSize: ax3, height: 2_000) { MailRouteHost(env: env, [.message(MockContent.invoiceID)]) }
        }
    }
    @Test func MS03_thread() async throws {
        try await withClock {
            let env = try await fixedEnv(reader: true)
            await Snapshot.check("MS03-thread", settle: slow) { MailRouteHost(env: env, [.message(MockContent.threadID)]) }
        }
    }
    @Test func AX3_MS03_thread() async throws {
        try await withClock {
            let env = try await fixedEnv(reader: true)
            await Snapshot.check("AX3-MS03-thread", settle: .milliseconds(3_500), typeSize: ax3, height: 2_000) { MailRouteHost(env: env, [.message(MockContent.threadID)]) }
        }
    }
    @Test func MS04_imagesBlocked() async throws {
        try await withClock {
            let env = try await fixedEnv(reader: true)
            await Snapshot.check("MS04-images-blocked", settle: slow) { MailRouteHost(env: env, [.message(MockContent.kettleID)]) }
        }
    }
    /// MS05: the attachment chips under the body (the QuickLook preview itself is a system controller and is not shot).
    @Test func MS05_attachment() async throws {
        try await withClock {
            let env = try await fixedEnv(reader: true)
            await Snapshot.check("MS05-attachment", settle: slow, height: 1_100) { MailRouteHost(env: env, [.message(MockContent.invoiceID)]) }
        }
    }
    @Test func MS06_moreSheet() async throws {
        try await withClock {
            let env = try await fixedEnv(reader: true)
            await Snapshot.check("MS06-more-sheet", settle: .milliseconds(3_000)) {
                BottomFrame { NavigationStack { MessageScreen(env: env, id: MockContent.invoiceID, showMore: true) { _ in } } }.inboxTheme(.system)
            }
        }
    }
    @Test func MS07_details() async throws {
        try await withClock {
            let env = try await fixedEnv(reader: true)
            await Snapshot.check("MS07-details") { MailRouteHost(env: env, [.message(MockContent.invoiceID), .details(MockContent.invoiceID)]) }
        }
    }
    @Test func AX3_MS07_details() async throws {
        try await withClock {
            let env = try await fixedEnv(reader: true)
            await Snapshot.check("AX3-MS07-details", settle: .milliseconds(3_500), typeSize: ax3, height: 2_200) { MailRouteHost(env: env, [.message(MockContent.invoiceID), .details(MockContent.invoiceID)]) }
        }
    }
    @Test func MS08_original() async throws {
        try await withClock {
            let env = try await fixedEnv(reader: true)
            await Snapshot.check("MS08-original") { MailRouteHost(env: env, [.message(MockContent.invoiceID), .original(MockContent.invoiceID)]) }
        }
    }
    @Test func MS09_gmailNotFound() async throws {
        try await withClock {
            let env = try await fixedEnv(reader: true)
            await Snapshot.check("MS09-gmail-not-found", settle: slow) { MailRouteHost(env: env, [.message(MockContent.notFoundID)]) }
        }
    }
    @Test func MS09b_detailsGmailNotFound() async throws {
        try await withClock {
            let env = try await fixedEnv(reader: true)
            await Snapshot.check("MS09b-details-gmail-not-found") { MailRouteHost(env: env, [.message(MockContent.notFoundID), .details(MockContent.notFoundID)]) }
        }
    }

    // MARK: Search: SR01-SR04

    @Test func SR01_idle() async throws {
        try await withClock {
            let env = try await fixedEnv()
            try await env.database.replaceRecentSearches(["northgate", "invoice october", "from:stripe"])
            await Snapshot.check("SR01-search-idle") {
                BottomFrame { NavigationStack { SearchScreen(env: env, scope: nil, useMockData: true, autoFocus: false, path: .constant([])) } }.inboxTheme(.system)
            }
        }
    }
    @Test func SR02_results() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await withDefaults(["OpenSearchQuery": "invoice"]) {
                await Snapshot.check("SR02-search-results", settle: slow) { MailRouteHost(env: env, [.search(scope: nil)]) }
            }
        }
    }
    @Test func AX3_SR02_results() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await withDefaults(["OpenSearchQuery": "invoice"]) {
                await Snapshot.check("AX3-SR02-search-results", settle: .milliseconds(3_500), typeSize: ax3, height: 2_400) { MailRouteHost(env: env, [.search(scope: nil)]) }
            }
        }
    }
    @Test func SR03_scoped() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await withDefaults(["OpenSearchQuery": "invoice"]) {
                await Snapshot.check("SR03-search-scoped", settle: slow) { MailRouteHost(env: env, [.search(scope: MockData.acme)]) }
            }
        }
    }
    @Test func SR04_noResults() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await withDefaults(["OpenSearchQuery": "zzqx"]) {
                await Snapshot.check("SR04-search-no-results", settle: slow) { MailRouteHost(env: env, [.search(scope: nil)]) }
            }
        }
    }

    // MARK: Needs attention: NA02-NA07

    @Test func NA02_unrouted() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await Snapshot.check("NA02-unrouted", settle: slow) { MailRouteHost(env: env, [.unrouted]) }
        }
    }
    @Test func AX3_NA02_unrouted() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await Snapshot.check("AX3-NA02-unrouted", settle: .milliseconds(3_500), typeSize: ax3, height: 2_400) { MailRouteHost(env: env, [.unrouted]) }
        }
    }
    @Test func NA03_ruleFromMessage() async throws {
        try await withClock {
            let env = try await fixedEnv()
            let list = try await env.api.unrouted()
            let g = try #require(list.groups.first)
            let msg = try #require(g.messages.first)
            await Snapshot.check("NA03-rule-from-message", settle: slow) {
                SheetHost(base: { NavigationStack { UnroutedScreen(env: env) }.inboxTheme(.system) },
                          sheet: { RuleFromMessageSheet(env: env, message: msg, businessName: g.business?.name, unroutedTotal: list.total) })
                    .inboxTheme(.system)
            }
        }
    }
    @Test func NA04_failed() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await Snapshot.check("NA04-failed", settle: slow) { MailRouteHost(env: env, [.failed]) }
        }
    }
    @Test func NA05_failedDetail() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await Snapshot.check("NA05-failed-detail", settle: slow) { MailRouteHost(env: env, [.failed, .failedDetail(MockRules.failedID)]) }
        }
    }
    @Test func AX3_NA05_failedDetail() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await Snapshot.check("AX3-NA05-failed-detail", settle: .milliseconds(3_500), typeSize: ax3, height: 2_400) { MailRouteHost(env: env, [.failed, .failedDetail(MockRules.failedID)]) }
        }
    }
    @Test func NA06_retrying() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await Snapshot.check("NA06-failed-retrying", settle: slow) {
                NavigationStack { FailedScreen(env: env, path: .constant([]), previewRetrying: true) }.inboxTheme(.system)
            }
        }
    }
    @Test func NA07_failedEmpty() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await withDefaults(["FailedState": "empty"]) {
                await Snapshot.check("NA07-failed-empty", settle: slow) { MailRouteHost(env: env, [.failed]) }
            }
        }
    }

    // MARK: Rules: RU02, RU05-RU10

    private func draft(_ env: AppEnvironment, existing: Bool) async throws -> RuleDraft {
        if existing, let r = try await env.database.rules().first { return RuleDraft(rule: r) }
        return RuleDraft()
    }

    @Test func RU02_rulesEdit() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await Snapshot.check("RU02-rules-edit", settle: slow) { RulesScreen(env: env, startEditing: true) { EmptyView() }.inboxTheme(.system) }
        }
    }
    @Test func RU05_editorNew() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await Snapshot.check("RU05-rule-editor-new") { NavigationStack { RuleEditorScreen(env: env, draft: RuleDraft(), path: .constant([])) }.inboxTheme(.system) }
        }
    }
    @Test func RU05_editorExisting() async throws {
        try await withClock {
            let env = try await fixedEnv()
            let d = try await draft(env, existing: true)
            await Snapshot.check("RU05-rule-editor-existing") { NavigationStack { RuleEditorScreen(env: env, draft: d, path: .constant([])) }.inboxTheme(.system) }
        }
    }
    @Test func AX3_RU05_editorExisting() async throws {
        try await withClock {
            let env = try await fixedEnv()
            let d = try await draft(env, existing: true)
            await Snapshot.check("AX3-RU05-rule-editor-existing", settle: slow, typeSize: ax3, height: 2_600) { NavigationStack { RuleEditorScreen(env: env, draft: d, path: .constant([])) }.inboxTheme(.system) }
        }
    }
    @Test func RU06_labelPicker() async throws {
        try await withClock {
            let env = try await fixedEnv()
            let d = try await draft(env, existing: true)
            await Snapshot.check("RU06-label-picker", settle: slow) {
                SheetHost(base: { NavigationStack { RuleEditorScreen(env: env, draft: d, path: .constant([])) }.inboxTheme(.system) },
                          sheet: { LabelPickerSheet(provider: env.labels, selection: .constant(d.label)) }).inboxTheme(.system)
            }
        }
    }
    @Test func RU07_editorInvalid() async throws {
        try await withClock {
            let env = try await fixedEnv()
            var d = try await draft(env, existing: true)
            d.subject = "invoice (#2"
            await Snapshot.check("RU07-rule-editor-invalid") { NavigationStack { RuleEditorScreen(env: env, draft: d, path: .constant([])) }.inboxTheme(.system) }
        }
    }
    @Test func RU08_testResults() async throws {
        try await withClock {
            let env = try await fixedEnv()
            var d = try await draft(env, existing: true)
            d.subject = "invoice"
            await Snapshot.check("RU08-rule-test", settle: slow) { NavigationStack { RuleTestScreen(env: env, draft: d) }.inboxTheme(.system) }
        }
    }
    @Test func AX3_RU08_testResults() async throws {
        try await withClock {
            let env = try await fixedEnv()
            var d = try await draft(env, existing: true)
            d.subject = "invoice"
            await Snapshot.check("AX3-RU08-rule-test", settle: .milliseconds(3_500), typeSize: ax3, height: 2_600) { NavigationStack { RuleTestScreen(env: env, draft: d) }.inboxTheme(.system) }
        }
    }
    @Test func RU09_testNothing() async throws {
        try await withClock {
            let env = try await fixedEnv()
            var d = RuleDraft()
            d.name = "Nothing"; d.from = "nobody@nowhere.invalid"; d.subject = "zzzzqq"
            await Snapshot.check("RU09-rule-test-none", settle: slow) { NavigationStack { RuleTestScreen(env: env, draft: d) }.inboxTheme(.system) }
        }
    }
    @Test func RU10_deleteAlert() async throws {
        try await withClock {
            let env = try await fixedEnv()
            let d = try await draft(env, existing: true)
            await Snapshot.check("RU10-rule-delete-alert", settle: .milliseconds(2_500)) { NavigationStack { RuleEditorScreen(env: env, draft: d, path: .constant([]), previewDelete: true) }.inboxTheme(.system) }
        }
    }

    // MARK: Gmail: GM01, GM04, rate limited, GM05, import states

    @Test func GM01_connected() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await withDefaults(["GmailState": "connected"]) {
                await Snapshot.check("GM01-gmail-connected", settle: slow) { DeviceFrame { NavigationStack { GmailScreen(env: env) } }.inboxTheme(.system) }
            }
        }
    }
    @Test func GM04_catchingUp() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await withDefaults(["GmailState": "catching_up"]) {
                await Snapshot.check("GM04-gmail-catching-up", settle: slow) { DeviceFrame { NavigationStack { GmailScreen(env: env) } }.inboxTheme(.system) }
            }
        }
    }
    @Test func GM04b_rateLimited() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await withDefaults(["GmailState": "rate_limited"]) {
                await Snapshot.check("GM04b-gmail-rate-limited", settle: slow) { DeviceFrame { NavigationStack { GmailScreen(env: env) } }.inboxTheme(.system) }
            }
        }
    }
    @Test func GM05_disconnectAlert() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await withDefaults(["GmailState": "connected"]) {
                await Snapshot.check("GM05-disconnect-alert", settle: .milliseconds(2_500)) { DeviceFrame { NavigationStack { GmailScreen(env: env, previewDisconnect: true) } }.inboxTheme(.system) }
            }
        }
    }
    @Test func AX3_GM01_connected() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await withDefaults(["GmailState": "catching_up"]) {
                await Snapshot.check("AX3-GM04-gmail-catching-up", settle: .milliseconds(3_500), typeSize: ax3, height: 2_000) { DeviceFrame { NavigationStack { GmailScreen(env: env) } }.inboxTheme(.system) }
            }
        }
    }
    @Test func GM06b_importPaused() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await withDefaults(["BackfillState": "paused"]) {
                await Snapshot.check("GM06b-import-paused", settle: slow) { NavigationStack { ImportOlderScreen(env: env) }.inboxTheme(.system) }
            }
        }
    }
    @Test func GM06c_importDone() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await withDefaults(["BackfillState": "done"]) {
                await Snapshot.check("GM06c-import-done", settle: slow) { NavigationStack { ImportOlderScreen(env: env) }.inboxTheme(.system) }
            }
        }
    }

    // MARK: Settings: Health, Alfather x4, Businesses, Business detail, Add business, Routing problem, ST06 delete sheet

    @Test func ST05_healthIssues() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await withDefaults(["HealthState": "needs_you"]) {
                await Snapshot.check("ST06-health-issues", settle: slow) { NavigationStack { HealthScreen(env: env) { _ in } }.inboxTheme(.system) }
            }
        }
    }
    @Test func ST05_healthOK() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await withDefaults(["HealthState": "ok"]) {
                await Snapshot.check("ST06-health-ok", settle: slow) { NavigationStack { HealthScreen(env: env) { _ in } }.inboxTheme(.system) }
            }
        }
    }
    @Test func AX3_ST05_healthIssues() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await withDefaults(["HealthState": "needs_you"]) {
                await Snapshot.check("AX3-ST06-health-issues", settle: .milliseconds(3_500), typeSize: ax3, height: 2_400) { NavigationStack { HealthScreen(env: env) { _ in } }.inboxTheme(.system) }
            }
        }
    }
    @Test func AF_alfatherStates() async throws {
        try await withClock {
            let env = try await fixedEnv()
            for (state, name) in [("not_paired", "AF01-alfather-setup"), ("connected", "AF02-alfather-connected"), ("reconnecting", "AF03-alfather-reconnecting"), ("unreachable", "AF04-alfather-unreachable")] {
                await withDefaults(["AlfatherState": state]) {
                    await Snapshot.check(name, settle: slow) { NavigationStack { AlfatherScreen(env: env, discovery: MockAlfatherDiscovery()) }.inboxTheme(.system) }
                }
            }
        }
    }
    @Test func AX3_AF04_alfatherUnreachable() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await withDefaults(["AlfatherState": "unreachable"]) {
                await Snapshot.check("AX3-AF04-alfather-unreachable", settle: .milliseconds(3_500), typeSize: ax3, height: 2_600) { NavigationStack { AlfatherScreen(env: env, discovery: MockAlfatherDiscovery()) }.inboxTheme(.system) }
            }
        }
    }
    @Test func BZ01_businessesSettings() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await Snapshot.check("BZ01-businesses-settings", settle: slow) { NavigationStack { BusinessesSettingsScreen(env: env, onOpen: { _ in }, onAdd: {}) }.inboxTheme(.system) }
        }
    }
    @Test func BZ03_businessDetail() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await Snapshot.check("BZ03-business-detail", settle: slow) { NavigationStack { BusinessDetailScreen(env: env, id: MockData.acme, onRouting: {}, onRemoved: {}) }.inboxTheme(.system) }
        }
    }
    @Test func BZ04_businessDetailRoutingProblem() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await Snapshot.check("BZ04-business-detail-routing", settle: slow) { NavigationStack { BusinessDetailScreen(env: env, id: MockData.lumen, onRouting: {}, onRemoved: {}) }.inboxTheme(.system) }
        }
    }
    /// DD-19: a 24-character name collides with "Businesses", so the back label reads "Back".
    @Test func BZ04b_businessDetailLongName() async throws {
        try await withClock {
            let env = try await fixedEnv()
            var rows = try await env.database.businesses()
            if let i = rows.firstIndex(where: { $0.id == MockData.lumen }) { rows[i].name = "Harbor & Pine Provisions" }
            try await env.database.replaceBusinesses(rows)
            await Snapshot.check("BZ04b-business-detail-long-name", settle: slow) { NavigationStack { BusinessDetailScreen(env: env, id: MockData.lumen, onRouting: {}, onRemoved: {}) }.inboxTheme(.system) }
        }
    }
    @Test func AX3_BZ03_businessDetail() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await Snapshot.check("AX3-BZ04-business-detail-routing", settle: .milliseconds(3_500), typeSize: ax3, height: 2_400) { NavigationStack { BusinessDetailScreen(env: env, id: MockData.lumen, onRouting: {}, onRemoved: {}) }.inboxTheme(.system) }
        }
    }
    @Test func BZ05_addBusiness() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await Snapshot.check("BZ05-add-business", settle: slow) { BottomFrame { NavigationStack { AddBusinessSheet(env: env) } }.inboxTheme(.system) }
        }
    }
    @Test func BZ06_routingProblem() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await Snapshot.check("BZ06-routing-problem", settle: slow) { NavigationStack { RoutingProblemScreen(env: env, id: MockData.lumen) }.inboxTheme(.system) }
        }
    }
    @Test func AX3_BZ06_routingProblem() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await Snapshot.check("AX3-BZ06-routing-problem", settle: .milliseconds(3_500), typeSize: ax3, height: 2_400) { NavigationStack { RoutingProblemScreen(env: env, id: MockData.lumen) }.inboxTheme(.system) }
        }
    }
    @Test func ST06_deleteSheet() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await withDefaults([:]) {
                UserDefaults.standard.set(true, forKey: "OpenDeleteSheet")
                defer { UserDefaults.standard.removeObject(forKey: "OpenDeleteSheet") }
                await Snapshot.check("ST04b-delete-sheet", settle: .milliseconds(3_000)) { NavigationStack { PrivacyScreen(env: env) }.inboxTheme(.system) }
            }
        }
    }
    @Test func ST06_deleteSheetEnabled() async throws {
        try await withClock {
            let env = try await fixedEnv()
            await withDefaults([:]) {
                UserDefaults.standard.set(true, forKey: "OpenDeleteSheet"); UserDefaults.standard.set(true, forKey: "OpenDeleteSheetTyped")
                defer { UserDefaults.standard.removeObject(forKey: "OpenDeleteSheet"); UserDefaults.standard.removeObject(forKey: "OpenDeleteSheetTyped") }
                await Snapshot.check("ST04b-delete-sheet-enabled", settle: .milliseconds(3_000)) { NavigationStack { PrivacyScreen(env: env) }.inboxTheme(.system) }
            }
        }
    }
}
