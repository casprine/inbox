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
            case .gmailConnected: GmailConnectedStep(model: model)
            case .filters: FiltersStep(model: model, env: env)
            case .backfill: BackfillStep(model: model)
            case .alfather: AlfatherStep(model: model)
            case .ready: ReadyStep(model: model)
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
                    HStack(spacing: 4) { Image(systemName: "chevron.left").font(.system(size: 19, weight: .semibold)); Text("Back").font(.inboxBody(17, medium: true)) }
                        .foregroundStyle(p.accentText).frame(minHeight: 44)
                }.buttonStyle(.plain)
                Spacer()
                if let skip { InboxToolbarButton("Skip", action: skip) }
            }
            .padding(.horizontal, 16)
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    Text("Step \(model.step.number) of \(OnboardingStep.total)").font(.inboxBody(13.5)).foregroundStyle(p.secondary)
                    Text(title).font(.inboxHeadingBold(27, relativeTo: .title2)).tracking(-0.5).foregroundStyle(p.text)
                    if let caption { Text(caption).font(.inboxBody(14.5)).lineSpacing(3).foregroundStyle(p.secondary) }
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
                Text("No domains found on this Cloudflare account.").font(.inboxBody(14.5)).foregroundStyle(p.secondary)
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
        if model.justRouted.contains(d.id) && d.selectable { return "Routed just now" }
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
                        .font(.inboxBody(14.5)).foregroundStyle(p.secondary)
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

// MARK: OB07 Gmail connected

/// "Step 3 of 7": the account that just connected and what Inbox may do with it. Continue goes on to the filters.
struct GmailConnectedStep: View {
    @Environment(\.palette) private var p
    let model: OnboardingModel
    var body: some View {
        let email = model.gmail.store.status?.email
        StepScaffold(model: model, title: "Gmail connected", caption: nil) {
            InsetGroup {
                HStack(spacing: 12) {
                    PersonGlyph().stroke(p.accent, style: StrokeStyle(lineWidth: 1.6 * 22 / 24, lineCap: .round, lineJoin: .round)).frame(width: 22, height: 22).frame(width: 24)
                        .accessibilityHidden(true)
                    VStack(alignment: .leading, spacing: 1) {
                        Text(email ?? "Your Gmail account").font(.inboxBody(16)).foregroundStyle(p.text).lineLimit(1).truncationMode(.middle)
                        HStack(spacing: 8) { StatusDot(.ok); Text("Read, label and archive").font(.inboxBody(13.5, relativeTo: .subheadline)).foregroundStyle(p.secondary) }
                    }
                    Spacer(minLength: 0)
                }
                .padding(.horizontal, 16).padding(.vertical, 11).frame(minHeight: 56)
                .accessibilityElement(children: .combine)
            }
            Text("You can disconnect at any time in Settings → Gmail.").font(.inboxBody(13, relativeTo: .footnote)).lineSpacing(2).foregroundStyle(p.secondary)
                .padding(.horizontal, 16).padding(.top, 2)
        } footer: {
            PrimaryButton("Continue") { Task { await model.continueFromGmailConnected() } }
        }
        .task { if model.gmail.store.status == nil { await model.gmail.load() } }
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
            if case .done(let n) = filters.phase { PrimaryButton("Continue") { Task { await model.finishFilters(imported: n) } } }
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
    @Environment(\.locale) private var locale
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
        let n = model.estimates[days].map { "About \($0.formatted(.number.locale(locale)))" } ?? "About —"
        return days == 90 ? "\(n) messages · recommended" : "\(n) messages"
    }
    private func radio(_ on: Bool) -> some View {
        ZStack {
            Circle().strokeBorder(on ? p.accent : p.hairlineStrong, lineWidth: 1.5)
            if on { Circle().fill(p.accent).frame(width: 12, height: 12) }
        }.frame(width: 22, height: 22)
    }
}

// MARK: OB12 You're set up

/// Last screen of setup: no navigation bar, a check, the summary, and "Open Inbox". Setup counts as done only after the button.
struct ReadyStep: View {
    @Environment(\.palette) private var p
    @Environment(\.dynamicTypeSize) private var typeSize
    let model: OnboardingModel

    private var message: some View {
        VStack(alignment: .leading, spacing: 0) {
            CheckMark().stroke(p.accent, style: StrokeStyle(lineWidth: 2.3, lineCap: .round, lineJoin: .round)).frame(width: 46, height: 46).accessibilityHidden(true)
            Text("You're set up.").font(.inboxHeadingBold(34, relativeTo: .largeTitle)).tracking(-0.8).lineSpacing(1).foregroundStyle(p.text)
                .fixedSize(horizontal: false, vertical: true).padding(.top, 16).accessibilityAddTraits(.isHeader)
            Text(model.summary).font(.inboxBody(17, relativeTo: .body)).lineSpacing(4).foregroundStyle(p.secondary)
                .fixedSize(horizontal: false, vertical: true).padding(.top, 14)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    var body: some View {
        Group {
            if typeSize.isAccessibilitySize {
                ScrollView {
                    VStack(alignment: .leading, spacing: 32) { message; open }.padding(.horizontal, 28).padding(.vertical, 32)
                }
            } else {
                ScrollView { message.padding(.horizontal, 28).padding(.top, 96) }
                    .safeAreaInset(edge: .bottom, spacing: 0) { open.padding(.horizontal, 20).padding(.bottom, 16).background(p.ground) }
            }
        }
        .task { await model.loadSummary() }
    }

    private var open: some View {
        PrimaryButton("Open Inbox", verticalPadding: typeSize.isAccessibilitySize ? 8 : 0) { Task { await model.openInbox() } }
    }
}

/// The board's person icon (circle head, shoulders), 24-unit grid.
struct PersonGlyph: Shape {
    func path(in r: CGRect) -> Path {
        let k = r.width / 24
        func pt(_ x: CGFloat, _ y: CGFloat) -> CGPoint { CGPoint(x: r.minX + x * k, y: r.minY + y * k) }
        var path = Path()
        path.addEllipse(in: CGRect(x: r.minX + 8.5 * k, y: r.minY + 5 * k, width: 7 * k, height: 7 * k))
        path.move(to: pt(5, 20))
        path.addCurve(to: pt(12, 14.5), control1: pt(6, 16.5), control2: pt(9, 14.5))
        path.addCurve(to: pt(19, 20), control1: pt(15, 14.5), control2: pt(18, 16.5))
        return path
    }
}

/// The 24-unit check from the board (`m5 12.5 4.5 4.5L19 7.5`) scaled to its frame.
struct CheckMark: Shape {
    func path(in r: CGRect) -> Path {
        func pt(_ x: CGFloat, _ y: CGFloat) -> CGPoint { CGPoint(x: r.minX + x / 24 * r.width, y: r.minY + y / 24 * r.height) }
        var path = Path()
        path.move(to: pt(5, 12.5)); path.addLine(to: pt(9.5, 17)); path.addLine(to: pt(19, 7.5))
        return path
    }
}

// MARK: OB10 / OB11 Alfather

struct AlfatherStep: View {
    @Environment(\.palette) private var p
    let model: OnboardingModel
    var body: some View {
        let found = model.alfatherHost
        if found == nil && !model.searchingAlfather { notFound } else { foundOrSearching(found) }
    }

    /// OB10: searching, or found and ready to pair.
    private func foundOrSearching(_ found: String?) -> some View {
        StepScaffold(model: model, title: found == nil ? "Looking for Alfather…" : "Alfather found",
                     caption: found == nil ? "Alfather is your local assistant. If it's running on this network, Inbox can hand it new mail to read in the background."
                     : "Your local assistant is running on this network. Inbox will pair with it; it then reads new mail from your Inbox server in the background.") {
            if let found {
                InsetGroup { InsetRow("Alfather", subtitle: "Found · \(found)") }
                Text("Nothing it reads shows in the app yet. That comes in a later version.").font(.inboxBody(14)).foregroundStyle(p.secondary)
            } else { ProgressView().padding(.top, 8) }
        } footer: {
            PrimaryButton("Continue") { Task { await model.finishAlfather(pair: found != nil) } }.disabled(model.searchingAlfather)
        }
        .task { if model.alfatherHost == nil { await model.searchAlfather() } }
    }

    /// OB11 (DD-7): Skip top right, one status row, a short checklist, hint, "Try again" / "Set up later".
    private var notFound: some View {
        StepScaffold(model: model, title: "Alfather not found",
                     caption: "Inbox works fully without it. Mail just won't be read in the background yet.",
                     skip: { Task { await model.finishAlfather(pair: false) } }) {
            VStack(alignment: .leading, spacing: 16) {
                InsetGroup {
                    InsetRow("Alfather", showsDivider: false) { Text("Not reachable").font(.inboxBody(16)).foregroundStyle(p.secondary) }
                }
                InsetGroup {
                    InsetRow("Alfather is running on your Mac", systemImage: "checkmark.circle")
                    InsetRow("This phone is on the same Wi\u{2060}-\u{2060}Fi network", showsDivider: false, systemImage: "checkmark.circle")
                }
                Text("You can pair later in Settings → Alfather.").font(.inboxBody(14)).foregroundStyle(p.secondary).padding(.horizontal, 4)
            }
        } footer: {
            PrimaryButton("Try again") { Task { await model.searchAlfather() } }
            PrimaryButton("Set up later", style: .plain) { Task { await model.finishAlfather(pair: false) } }
        }
    }
}
