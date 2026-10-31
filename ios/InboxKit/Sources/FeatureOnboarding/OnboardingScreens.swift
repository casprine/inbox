import SwiftUI
import API
import DesignSystem
import Domain
import FeatureGmail

/// Onboarding root (OB01–OB13). Shown until `onboarding.step == done`; `onFinish` hands over to the tab shell.
public struct OnboardingFlow: View {
    @Environment(\.palette) private var p
    @State private var model: OnboardingModel
    let onFinish: () -> Void
    private let env: AppEnvironment

    public init(env: AppEnvironment, authenticator: any WebAuthenticating = SystemWebAuthenticator(), defaults: UserDefaults = .standard,
                estimator: (any BackfillEstimating)? = nil, discovery: any AlfatherDiscovering = BonjourAlfatherDiscovery(),
                onFinish: @escaping () -> Void) {
        self.env = env; self.onFinish = onFinish
        _model = State(initialValue: OnboardingModel(env: env, authenticator: authenticator, defaults: defaults, estimator: estimator, discovery: discovery))
    }

    public var body: some View {
        Group {
            if let signIn = model.signIn {
                SignInStep(onboarding: model, model: signIn).transition(.move(edge: .trailing))
            } else { stepView }
        }
        .background(p.ground)
        .animation(.easeOut(duration: 0.2), value: model.step)
        .animation(.easeOut(duration: 0.25), value: model.signIn != nil)
        .task {
            await model.resume()
            // Screenshot aid (mock mode): `-OnboardingStep domains|gmail|filters|backfill|alfather` opens that step.
            if let raw = UserDefaults.standard.string(forKey: "OnboardingStep"), let s = OnboardingStep(rawValue: raw) { model.jump(to: s) }
            // `-OpenSignIn new|existing|signedOut` opens the sign-in screen (screenshots, UI tests).
            switch UserDefaults.standard.string(forKey: "OpenSignIn") {
            case "new": model.openSignIn(.new)
            case "existing": model.openSignIn(.existing)
            case "signedOut": model.openSignIn(.signedOut)
            default: break
            }
        }
    }

    @ViewBuilder private var stepView: some View {
        Group {
            switch model.step {
            case .welcome: WelcomeStep(model: model)
            case .domains: DomainsStep(model: model)
            case .gmail: GmailStep(model: model)
            case .filters: FiltersStep(model: model, env: env)
            case .backfill: BackfillStep(model: model)
            case .alfather: AlfatherStep(model: model)
            case .done: Color.clear.onAppear(perform: onFinish)
            }
        }
    }
}

// MARK: Scaffold

struct StepScaffold<Content: View, Footer: View>: View {
    @Environment(\.palette) private var p
    let model: OnboardingModel
    let title: String
    let caption: String?
    var skip: (() -> Void)?
    @ViewBuilder var content: Content
    @ViewBuilder var footer: Footer

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Button { model.back() } label: {
                    HStack(spacing: 4) { Image(systemName: "chevron.left").font(.system(size: 19, weight: .semibold)); Text("Back").font(.inboxBody(17)) }
                        .foregroundStyle(p.accentText).frame(minHeight: 44)
                }.buttonStyle(.plain)
                Spacer()
                if let skip { InboxToolbarButton("Skip", action: skip) }
            }
            .padding(.horizontal, 16)
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    Text("Step \(model.step.number) of \(OnboardingStep.total)").font(.inboxBody(13.5)).foregroundStyle(p.secondary)
                    Text(title).font(.inboxHeading(28, relativeTo: .title)).tracking(-0.4).foregroundStyle(p.text)
                    if let caption { Text(caption).font(.inboxBody(15)).lineSpacing(3).foregroundStyle(p.secondary) }
                    if let b = model.banner { NoticeBanner(notice: b) }
                    content.padding(.horizontal, -4)   // cards sit on the 16 pt gutter, text stays on 20
                }
                .padding(.horizontal, 20).padding(.top, 8).padding(.bottom, 24)
            }
            VStack(spacing: 10) { footer }.padding(.horizontal, 16).padding(.bottom, 16)
        }
    }
}

struct NoticeBanner: View {
    let notice: OnboardingModel.Notice
    var body: some View { Banner(.warning, title: notice.title, message: notice.message, systemImage: "exclamationmark.triangle") }
}

// MARK: OB01 Welcome

/// Both buttons only navigate to the sign-in screen (D4 §1.1); nothing here signs in. From AX1 the buttons and footer line move
/// into the scroll content so large text never pushes them off screen.
struct WelcomeStep: View {
    @Environment(\.palette) private var p
    @Environment(\.dynamicTypeSize) private var typeSize
    let model: OnboardingModel

    private var intro: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("One quiet inbox for all your businesses.").font(.inboxHeadingBold(32, relativeTo: .largeTitle)).tracking(-0.8).foregroundStyle(p.text)
                .fixedSize(horizontal: false, vertical: true).accessibilityAddTraits(.isHeader)
            Text("Mail to your domains still lands in Gmail. Inbox reads a copy, sorts it by business, and keeps Gmail tidy.")
                .font(.inboxBody(16.5)).lineSpacing(3).foregroundStyle(p.secondary).padding(.top, 16).fixedSize(horizontal: false, vertical: true)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private var actions: some View {
        VStack(spacing: 0) {
            PrimaryButton("Get started", verticalPadding: typeSize.isAccessibilitySize ? 8 : 0) { model.openSignIn(.new) }
            PrimaryButton("I already set this up", style: .plain, verticalPadding: typeSize.isAccessibilitySize ? 8 : 0) { model.openSignIn(.existing) }
            Text("Next you'll enter your server address and password.").font(.inboxBody(13.5, relativeTo: .footnote)).foregroundStyle(p.secondary)
                .multilineTextAlignment(.center).fixedSize(horizontal: false, vertical: true).frame(maxWidth: .infinity).padding(.top, 8)
        }
    }

    var body: some View {
        if typeSize.isAccessibilitySize {
            ScrollView {
                VStack(alignment: .leading, spacing: 32) { intro; actions }
                    .padding(.horizontal, 24).padding(.vertical, 32)
            }
        } else {
            ScrollView { intro.padding(.horizontal, 24).padding(.top, 48) }
                .defaultScrollAnchor(.center)
                .safeAreaInset(edge: .bottom, spacing: 0) {
                    actions.padding(.horizontal, 24).padding(.bottom, 16).background(p.ground)
                }
        }
    }
}

// MARK: OB02 / OB03 Choose businesses, Fix routing

struct DomainsStep: View {
    @Environment(\.palette) private var p
    let model: OnboardingModel
    var body: some View {
        StepScaffold(model: model, title: "Which domains are your businesses?", caption: "Each one becomes its own mailbox. You can change this later.") {
            if model.domainsLoading && model.domains.isEmpty { ProgressView().frame(maxWidth: .infinity).padding(.top, 24) }
            InsetGroup("On Cloudflare") {
                ForEach(Array(model.domains.enumerated()), id: \.element.id) { i, d in row(d, divider: i < model.domains.count - 1) }
            }
            if model.domains.isEmpty && !model.domainsLoading && model.banner == nil {
                Text("No domains found on this Cloudflare account.").font(.inboxBody(15)).foregroundStyle(p.secondary)
            }
        } footer: {
            PrimaryButton(model.chosen.count == 1 ? "Continue with 1 business" : "Continue with \(model.chosen.count) businesses") { Task { await model.continueWithDomains() } }
                .disabled(model.chosen.isEmpty || model.saving).opacity(model.chosen.isEmpty ? 0.4 : 1)
        }
        .sheet(item: Binding(get: { model.fixing }, set: { model.fixing = $0 })) { d in FixRoutingSheet(model: model, domain: d).environment(\.palette, p) }
        .task { if model.domains.isEmpty { await model.loadDomains() } }
    }

    private func row(_ d: CloudflareDomain, divider: Bool) -> some View {
        HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 2) {
                Text(d.domain).font(.inboxBody(16.5)).foregroundStyle(d.selectable || d.alreadyAdded ? p.text : p.secondary)
                Text(subline(d)).font(.inboxBody(14)).foregroundStyle(p.secondary)
            }
            Spacer()
            if d.selectable { SquareSwitch(isOn: Binding(get: { model.selected.contains(d.id) }, set: { _ in model.toggle(d) })).accessibilityLabel(d.domain) }
            else if !d.alreadyAdded && d.routing != .ok { Button("Fix") { model.fixing = d }.font(.inboxBody(16, medium: true)).foregroundStyle(p.accentText).frame(minHeight: 44) }
        }
        .padding(.horizontal, 16).frame(minHeight: 56)
        .overlay(alignment: .bottom) { if divider { Rectangle().fill(p.hairline).frame(height: 1).padding(.leading, 16) } }
        .accessibilityElement(children: .contain)
    }

    private func subline(_ d: CloudflareDomain) -> String {
        if d.alreadyAdded { return "Already added" }
        switch d.routing {
        case .ok: return d.selectable ? "Ready" : d.status == "active" ? "Ready" : "Not active on Cloudflare"
        case .toEmail: return "Catch-all still sends to email."
        case .none: return "Email Routing isn't on for this domain."
        case .unknown: return "Couldn't check routing."
        }
    }
}

struct FixRoutingSheet: View {
    @Environment(\.palette) private var p
    @Environment(\.dismiss) private var dismiss
    @Environment(\.openURL) private var openURL
    let model: OnboardingModel
    let domain: CloudflareDomain
    var body: some View {
        VStack(spacing: 0) {
            SheetHeader("Route \(domain.domain) to Inbox", trailingTitle: "Close") { dismiss() }
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    Text(domain.routing == .none ? "Email Routing isn't set up for this domain, so Inbox never sees its mail."
                         : "Its catch-all still sends straight to email, so Inbox never sees that mail.")
                        .font(.inboxBody(15)).foregroundStyle(p.secondary)
                    InsetGroup("In Cloudflare") {
                        step("1 · Open Email Routing", "Cloudflare dashboard → \(domain.domain)")
                        step("2 · Edit the catch-all rule", "Action: Send to a Worker")
                        step("3 · Choose the worker", "inbox-worker")
                        step("4 · Save", nil, divider: false)
                    }
                    Text("Mail keeps arriving in Gmail as before. Nothing changes for senders, and Gmail still gets every message first.")
                        .font(.inboxBody(14)).foregroundStyle(p.secondary)
                    if model.fixing?.routing == .toEmail || model.fixing?.routing == Optional.some(.none) { EmptyView() }
                    PrimaryButton("Open Cloudflare") { openURL(URL(string: "https://dash.cloudflare.com")!) }
                    PrimaryButton(model.checking ? "Checking…" : "Check again", style: .secondary) { Task { await model.checkAgain(domain) } }.disabled(model.checking)
                }.padding(16)
            }
        }
        .background(p.ground)
    }
    private func step(_ t: String, _ s: String?, divider: Bool = true) -> some View { InsetRow(t, subtitle: s, showsDivider: divider) }
}

// MARK: OB05 Gmail

struct GmailStep: View {
    @Environment(\.palette) private var p
    let model: OnboardingModel
    var body: some View {
        StepScaffold(model: model, title: "Connect Gmail", caption: "Inbox never sends mail. It needs Gmail only to keep it tidy.") {
            if let m = model.gmail.message { Banner(.error, title: m, systemImage: "exclamationmark.triangle") }
            list("Inbox will", ["Label mail by business and rule", "Archive mail your rules archive", "Import older mail once"])
            list("Inbox won't", ["Send, delete or forward anything", "Share your mail with anyone"])
            if model.gmail.isWorking {
                VStack(alignment: .leading, spacing: 6) {
                    Text("Continue in your browser").font(.inboxBody(16, medium: true)).foregroundStyle(p.text)
                    Text("Google asks you to approve access there. You'll come straight back here.").font(.inboxBody(14)).foregroundStyle(p.secondary)
                }
            }
        } footer: {
            PrimaryButton(model.gmail.isWorking ? "Waiting for Google…" : "Continue with Google") { Task { await model.connectGmail() } }.disabled(model.gmail.isWorking)
            PrimaryButton("Skip for now", style: .plain) { Task { await model.skipGmail() } }
        }
    }
    private func list(_ title: String, _ items: [String]) -> some View {
        InsetGroup(title) { ForEach(Array(items.enumerated()), id: \.offset) { i, t in InsetRow(t, showsDivider: i < items.count - 1) } }
    }
}

// MARK: OB08 Filters

struct FiltersStep: View {
    @Environment(\.palette) private var p
    let model: OnboardingModel
    @State private var filters: ImportFiltersModel
    init(model: OnboardingModel, env: AppEnvironment) { self.model = model; _filters = State(initialValue: ImportFiltersModel(env: env)) }

    var body: some View {
        StepScaffold(model: model, title: "Bring your Gmail filters",
                     caption: "Picked filters become rules. They start in label-only mode, so they never fight Gmail.",
                     skip: { Task { await model.finishFilters() } }) {
            ImportFiltersContent(model: filters)
        } footer: {
            if case .done = filters.phase { PrimaryButton("Continue") { Task { await model.finishFilters() } } }
            else if case .loaded = filters.phase, !filters.importable.isEmpty {
                PrimaryButton(filters.isImporting ? "Importing…" : "Import \(filters.selected.count) as rules") { Task { await filters.run() } }
                    .disabled(filters.selected.isEmpty || filters.isImporting).opacity(filters.selected.isEmpty ? 0.4 : 1)
            } else { PrimaryButton("Continue") { Task { await model.finishFilters() } } }
        }
        .task { await filters.load() }
    }
}

// MARK: OB09 Backfill

struct BackfillStep: View {
    @Environment(\.palette) private var p
    let model: OnboardingModel
    private let options: [(days: Int, title: String)] = [(30, "Last 30 days"), (90, "Last 90 days"), (365, "Last year")]
    var body: some View {
        StepScaffold(model: model, title: "Import older mail?", caption: "A one-time copy from Gmail so each business isn't empty on day one.") {
            InsetGroup("Import") {
                ForEach(Array(options.enumerated()), id: \.element.days) { i, o in
                    Button { model.backfillDays = o.days } label: {
                        HStack {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(o.title).font(.inboxBody(16.5)).foregroundStyle(p.text)
                                Text(sub(o.days)).font(.inboxBody(14)).foregroundStyle(p.secondary)
                            }
                            Spacer()
                            radio(model.backfillDays == o.days)
                        }
                        .padding(.horizontal, 16).frame(minHeight: 60).contentShape(Rectangle())
                        .overlay(alignment: .bottom) { Rectangle().fill(p.hairline).frame(height: 1).padding(.leading, 16) }
                    }.buttonStyle(.plain).accessibilityAddTraits(model.backfillDays == o.days ? .isSelected : [])
                }
                Button { model.backfillDays = nil } label: {
                    HStack {
                        VStack(alignment: .leading, spacing: 2) {
                            Text("Don't import").font(.inboxBody(16.5)).foregroundStyle(p.text)
                            Text("Start with new mail only").font(.inboxBody(14)).foregroundStyle(p.secondary)
                        }
                        Spacer(); radio(model.backfillDays == nil)
                    }.padding(.horizontal, 16).frame(minHeight: 60).contentShape(Rectangle())
                }.buttonStyle(.plain).accessibilityAddTraits(model.backfillDays == nil ? .isSelected : [])
            }
            Text("Runs in the background. You can start using Inbox right away.").font(.inboxBody(14)).foregroundStyle(p.secondary)
        } footer: {
            PrimaryButton(model.backfillDays.map { "Import \($0 == 365 ? "a year" : "\($0) days")" } ?? "Continue") {
                Task { await model.finishBackfill(start: model.backfillDays != nil) }
            }
        }
        .task { await model.loadEstimates() }
    }
    private func sub(_ days: Int) -> String {
        let n = model.estimates[days].map { "About \($0.formatted())" } ?? "About —"
        return days == 90 ? "\(n) messages · recommended" : "\(n) messages"
    }
    private func radio(_ on: Bool) -> some View {
        ZStack {
            Circle().strokeBorder(on ? p.accent : p.hairlineStrong, lineWidth: 1.5)
            if on { Circle().fill(p.accent).frame(width: 12, height: 12) }
        }.frame(width: 22, height: 22)
    }
}

// MARK: OB10 / OB11 Alfather

struct AlfatherStep: View {
    @Environment(\.palette) private var p
    let model: OnboardingModel
    var body: some View {
        let found = model.alfatherHost
        StepScaffold(model: model, title: found == nil ? (model.searchingAlfather ? "Looking for Alfather…" : "Alfather not found") : "Alfather found",
                     caption: found == nil ? "Alfather is your local assistant. If it's running on this network, Inbox can hand it new mail to read in the background."
                     : "Your local assistant is running on this network. Inbox will pair with it; it then reads new mail from your Inbox server in the background.") {
            if let found {
                InsetGroup { InsetRow("Alfather", subtitle: "Found · \(found)") }
                Text("Nothing it reads shows in the app yet. That comes in a later version.").font(.inboxBody(14)).foregroundStyle(p.secondary)
            } else if model.searchingAlfather { ProgressView().padding(.top, 8) }
            else { Text("You can pair later in Settings → Alfather.").font(.inboxBody(14)).foregroundStyle(p.secondary) }
        } footer: {
            PrimaryButton(found == nil ? "Finish" : "Continue") { Task { await model.finishAlfather(pair: found != nil) } }.disabled(model.searchingAlfather)
            if found == nil && !model.searchingAlfather { PrimaryButton("Look again", style: .plain) { Task { await model.searchAlfather() } } }
        }
        .task { if model.alfatherHost == nil { await model.searchAlfather() } }
    }
}
