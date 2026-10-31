import Foundation
import UIKit
import Observation
import API
import Domain
import Persistence
import FeatureGmail

/// The seven onboarding steps (OB01–OB13). Raw values are stored in `pref.onboarding.step` so the flow resumes.
public enum OnboardingStep: String, CaseIterable, Sendable {
    case welcome, domains, gmail, gmailConnected, filters, backfill, alfather, ready, done
    /// "Step n of 7" shown from step 2 on. OB07 "Gmail connected" is the second half of step 3; OB12 "You're set up" is the end (no counter).
    public var number: Int {
        switch self {
        case .welcome: 1
        case .domains: 2
        case .gmail, .gmailConnected: 3
        case .filters: 4
        case .backfill: 5
        case .alfather: 6
        case .ready, .done: 7
        }
    }
    public static let total = 7
    public var previous: OnboardingStep? { Self.allCases.firstIndex(of: self).flatMap { $0 > 0 ? Self.allCases[$0 - 1] : nil } }
}

public enum OnboardingState {
    public static let key = "onboarding.step"
    public static func step(_ db: AppDatabase) async -> OnboardingStep {
        (try? await db.pref(key)).flatMap { $0 }.flatMap(OnboardingStep.init(rawValue:)) ?? .welcome
    }
    public static func isComplete(_ db: AppDatabase) async -> Bool { await step(db) == .done }

    /// Setup finished on this phone earlier and a server address is remembered, but the device token is gone (revoked, expired, cleared):
    /// open the app signed out (Mail from the cache plus "You've been signed out") instead of the welcome screen.
    public static func hasLostSession(env: AppEnvironment, defaults: UserDefaults = .standard) async -> Bool {
        guard await env.tokenStore.token() == nil, await isComplete(env.database) else { return false }
        return !(defaults.string(forKey: SignInModel.addressKey) ?? "").isEmpty
    }

    /// Whether to show onboarding at launch. A seeded dev token (`INBOX_DEV_TOKEN`, debug builds) counts as a finished sign-in and
    /// onboarding, so a fresh install goes straight to Mail; otherwise onboarding shows until a token exists and the step is `done`.
    public static func needsOnboarding(env: AppEnvironment, devToken: String?) async -> Bool {
        if let dev = devToken, !dev.isEmpty {
            try? await env.tokenStore.save(dev)
            try? await env.database.setPref(key, OnboardingStep.done.rawValue)
            return false
        }
        let hasToken = await env.tokenStore.token() != nil
        let complete = await isComplete(env.database)
        return !(hasToken && complete)
    }
}

@MainActor @Observable
public final class OnboardingModel {
    public private(set) var step: OnboardingStep = .welcome
    // OB01b: the server sign-in screen, pushed over the welcome step while non-nil
    public private(set) var signIn: SignInModel?
    public private(set) var banner: Notice?
    // OB02/OB03
    public private(set) var domains: [CloudflareDomain] = []
    public private(set) var domainsLoading = false
    public var selected: Set<String> = []          // zone ids
    public var fixing: CloudflareDomain?
    public private(set) var checking = false
    /// Zones whose routing was fixed in this session (OB04 shows "Routed just now").
    public private(set) var justRouted: Set<String> = []
    // OB05–OB08
    public let gmail: GmailModel
    // OB09
    public private(set) var estimates: [Int: Int] = [:]
    public var backfillDays: Int? = 90
    // OB10
    public private(set) var alfatherService: AlfatherService?
    public var alfatherHost: String? { alfatherService?.host }
    public private(set) var searchingAlfather = false
    public private(set) var saving = false
    // OB07 / OB12
    /// Rules created from Gmail filters in OB08 (this run of the flow).
    public private(set) var importedRules = 0
    public private(set) var summary = ""

    public struct Notice: Equatable, Sendable { public var title: String; public var message: String?; public var retry: Bool }

    private let env: AppEnvironment
    private let defaults: UserDefaults
    private let estimator: any BackfillEstimating
    private let discovery: any AlfatherDiscovering

    /// `defaults` holds the remembered server address; tests and mock mode pass a throwaway suite.
    public init(env: AppEnvironment, authenticator: any WebAuthenticating = SystemWebAuthenticator(), defaults: UserDefaults = .standard,
                estimator: (any BackfillEstimating)? = nil, discovery: any AlfatherDiscovering = BonjourAlfatherDiscovery()) {
        self.env = env; self.defaults = defaults; self.estimator = estimator ?? APIBackfillEstimator(api: env.api); self.discovery = discovery
        gmail = GmailModel(env: env, authenticator: authenticator)
    }

    public func resume() async {
        let saved = await OnboardingState.step(env.database)
        // A stored step past the welcome screen only makes sense while we still hold a device token.
        step = (await env.tokenStore.token() == nil) ? .welcome : saved
    }

    private func go(_ s: OnboardingStep) async {
        step = s
        try? await env.database.setPref(OnboardingState.key, s.rawValue)
    }

    /// Launch aid for screenshots and previews.
    public func jump(to s: OnboardingStep) { step = s }

    public func back() {
        if signIn != nil { signIn = nil; return }
        if let p = step.previous, step != .welcome { step = p }
    }

    // MARK: OB01 / OB01b

    /// "Get started" and "I already set this up" only navigate: they open the sign-in screen in the matching mode.
    public func openSignIn(_ mode: SignInMode) {
        banner = nil
        signIn = SignInModel(env: env, mode: mode, defaults: defaults)
    }

    /// After a successful `POST /v1/auth/login` (the device token is already in the Keychain): the destination depends on the mode.
    /// New: choose businesses (OB02). Existing: resume at the server's `setup_step` (done: Mail, gmail: Gmail connect, domains: domains).
    /// A server that doesn't send `setup_step` is read from its business list instead: Mail when it already has businesses, otherwise OB02.
    public func signedIn(_ mode: SignInMode, setupStep: String? = nil) async {
        switch mode {
        case .new:
            await go(.domains); signIn = nil; await loadDomains()
        case .existing:
            switch Self.resumeStep(setupStep) {
            case .done?: await go(.done); signIn = nil
            case .gmail?: await go(.gmail); signIn = nil
            case .domains?: await go(.domains); signIn = nil; await loadDomains()
            default:
                let cached = await env.database.hasBusinesses()
                let remote = ((try? await env.api.businesses())?.isEmpty == false)
                if cached || remote { await go(.done); signIn = nil } else { await go(.domains); signIn = nil; await loadDomains() }
            }
        case .signedOut:
            signIn = nil
            await go(.done)
        }
    }

    /// The three `setup_step` values a server reports; anything else (absent, unknown) falls back to the business-list guess.
    nonisolated static func resumeStep(_ raw: String?) -> OnboardingStep? {
        switch raw { case "done": .done; case "gmail": .gmail; case "domains": .domains; default: nil }
    }

    // MARK: OB02 / OB03

    public func loadDomains() async {
        domainsLoading = true; defer { domainsLoading = false }
        do {
            domains = try await env.api.cloudflareDomains()
            selected = selected.intersection(Set(domains.filter(\.selectable).map(\.id)))
            if selected.isEmpty { selected = Set(domains.filter(\.selectable).map(\.id)) }
            banner = nil
        } catch let e as APIError {
            if case .problem(let p, _) = e, p.code == "cloudflare_not_configured" {
                banner = Notice(title: "Cloudflare isn't connected on your server", message: "Add the Cloudflare token to the server settings, then try again.", retry: true)
            } else { banner = Notice(title: "Couldn't load your domains", message: "Check your connection and try again.", retry: true) }
        } catch { banner = Notice(title: "Couldn't load your domains", message: nil, retry: true) }
    }

    public func toggle(_ d: CloudflareDomain) {
        guard d.selectable else { return }
        if selected.contains(d.id) { selected.remove(d.id) } else { selected.insert(d.id) }
    }

    /// OB03 "Check again": re-reads the zone; when routing is fixed the switch (OB04) turns on.
    public func checkAgain(_ d: CloudflareDomain) async {
        checking = true; defer { checking = false }
        guard let fresh = try? await env.api.checkCloudflareDomain(zoneId: d.zoneId) else { return }
        if let i = domains.firstIndex(where: { $0.id == d.id }) { domains[i] = fresh }
        if fresh.selectable { selected.insert(fresh.id); justRouted.insert(fresh.id); fixing = nil } else { fixing = fresh }
    }

    public var chosen: [CloudflareDomain] { domains.filter { selected.contains($0.id) } }

    /// Business name from a domain: "acme-studio.com" → "Acme Studio".
    public nonisolated static func name(forDomain domain: String) -> String {
        let label = domain.split(separator: ".").first.map(String.init) ?? domain
        return label.split(whereSeparator: { $0 == "-" || $0 == "_" }).map { $0.prefix(1).uppercased() + $0.dropFirst() }.joined(separator: " ")
    }

    public func continueWithDomains() async {
        guard !chosen.isEmpty, !saving else { return }
        saving = true; defer { saving = false }
        do {
            for d in chosen where !d.alreadyAdded {
                _ = try await env.api.createBusiness(CreateBusinessRequest(name: Self.name(forDomain: d.domain), domain: d.domain, cfZoneId: d.zoneId))
            }
            try? await env.businesses.refresh()
            banner = nil
            await go(.gmail)
        } catch { banner = Notice(title: "Couldn't add your businesses", message: "Nothing was changed. Try again.", retry: true) }
    }

    // MARK: OB05–OB08

    public func connectGmail() async {
        await gmail.connect()
        if env.gmail.isConnected { await go(.gmailConnected) }
    }
    /// OB07 Continue.
    public func continueFromGmailConnected() async { await go(.filters) }
    public func skipGmail() async { await go(.alfather) }          // Gmail skipped: no filters or import to offer
    public func finishFilters(imported: Int = 0) async { importedRules = imported; await go(.backfill) }

    // MARK: OB09

    public func loadEstimates() async { estimates = await estimator.estimates() }

    public func finishBackfill(start: Bool) async {
        if start, let d = backfillDays {
            try? await env.database.setPref("onboarding.backfill_days", String(d))
            // The real job runs in the background; the first-open banner follows it (SSE job.progress).
            if (try? await env.api.startBackfill(days: d)) != nil { env.backfill.noteStarted(days: d); await env.backfill.refresh(env.api) }
        } else { try? await env.database.setPref("onboarding.backfill_days", "") }
        await go(.alfather)
    }

    // MARK: OB10 / OB11

    public func searchAlfather() async {
        searchingAlfather = true; defer { searchingAlfather = false }
        alfatherService = await discovery.discover(timeout: .seconds(4))
    }

    public func finishAlfather(pair: Bool) async {
        if pair, let s = alfatherService {
            do { try await AlfatherPairer(api: env.api, discovery: discovery).pair(s) }
            catch { banner = Notice(title: "Couldn't pair", message: "Alfather didn't accept the pairing. Check it's running and try again.", retry: true); return }
        }
        banner = nil
        await finish()
    }

    /// End of setup: show OB12 "You're set up".
    public func finish() async { await go(.ready); await loadSummary() }
    /// OB12 "Open Inbox": setup is complete, hand over to the tab shell.
    public func openInbox() async { await go(.done) }

    // MARK: OB12

    /// The one-line summary under "You're set up.": businesses on the server, rules imported from Gmail filters, the chosen import range.
    public func loadSummary() async {
        let businesses = ((try? await env.database.businesses()) ?? []).count
        let days = Int((try? await env.database.pref("onboarding.backfill_days")) ?? "") ?? 0
        summary = Self.summary(businesses: businesses, rules: importedRules, backfillDays: days)
    }

    /// "4 businesses, 3 rules from Gmail, and 90 days of mail on its way." Parts that don't apply are left out.
    nonisolated static func summary(businesses: Int, rules: Int, backfillDays: Int) -> String {
        var parts: [String] = []
        if businesses > 0 { parts.append(businesses == 1 ? "1 business" : "\(businesses) businesses") }
        if rules > 0 { parts.append(rules == 1 ? "1 rule from Gmail" : "\(rules) rules from Gmail") }
        if backfillDays > 0 { parts.append((backfillDays >= 365 ? "a year" : "\(backfillDays) days") + " of mail on its way") }
        switch parts.count {
        case 0: return "New mail will start to arrive here."
        case 1: return parts[0] + "."
        case 2: return parts[0] + " and " + parts[1] + "."
        default: return parts.dropLast().joined(separator: ", ") + ", and " + parts[parts.count - 1] + "."
        }
    }
}

extension AppDatabase {
    func hasBusinesses() async -> Bool { ((try? await businesses()) ?? []).isEmpty == false }
}
