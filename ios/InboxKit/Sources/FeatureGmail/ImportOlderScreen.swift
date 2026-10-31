import SwiftUI
import API
import DesignSystem
import Domain

public struct ImportOlderScreen: View {
    @Environment(\.palette) private var p
    @Environment(\.locale) private var locale
    @State private var model: ImportOlderModel
    @State private var pickingBusinesses = false
    private let back: String

    public init(env: AppEnvironment, back: String = "Settings") { _model = State(initialValue: ImportOlderModel(env: env)); self.back = back }

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                if let m = model.message { Banner(.warning, title: m, systemImage: "exclamationmark.triangle") }
                if model.store.isActive { running }
                else if model.store.batchFinished && !model.store.latestBatch.isEmpty && model.store.hasLoaded && showsDone { done }
                else { setup }
            }.padding(16)
        }
        .background(p.ground)
        .inboxPushed(title: "Import older mail", back: back)
        .sheet(isPresented: $pickingBusinesses, onDismiss: { Task { await model.refreshEstimate() } }) { picker.environment(\.palette, p).presentationDetents([.medium]) }
        .alert("Cancel the import?", isPresented: $model.confirmingCancel) {
            Button("Cancel import", role: .destructive) { Task { await model.cancel() } }
            Button("Keep importing", role: .cancel) {}
        } message: { Text("Mail already imported stays. The rest isn't fetched.") }
        .task { await model.load() }
    }

    @State private var showsDone = true

    // MARK: GM06 setup

    private var setup: some View {
        VStack(alignment: .leading, spacing: 18) {
            Text("A one-time copy from Gmail so each business isn't empty. Imported mail is labelled only; nothing is archived or deleted in Gmail.")
                .font(.inboxBody(15)).foregroundStyle(p.secondary)
            InsetGroup("Range") {
                option("Last 30 days", .days(30)); option("Last 90 days", .days(90)); option("Last year", .days(365))
                option("Custom…", .custom, divider: model.range == .custom)
                if model.range == .custom {
                    DatePicker("Since", selection: $model.customSince, in: ...AppClock.now, displayedComponents: .date)
                        .font(.inboxBody(16.5)).padding(.horizontal, 16).frame(minHeight: 52)
                        .onChange(of: model.customSince) { Task { await model.refreshEstimate() } }
                }
            }
            InsetGroup("Businesses") {
                Button { pickingBusinesses = true } label: {
                    InsetRow(model.businessSummary, showsDivider: false) { Image(systemName: "chevron.right").font(.system(size: 14, weight: .semibold)).foregroundStyle(p.hairlineStrong) }
                }.buttonStyle(.plain)
            }
            Text(estimateText).font(.inboxBody(14)).foregroundStyle(p.secondary).padding(.horizontal, 4)
            PrimaryButton(model.starting ? "Starting…" : "Start import") { Task { await model.start() } }
                .disabled(model.starting || model.estimate == nil).opacity(model.estimate == nil ? 0.5 : 1)
        }
    }

    private var estimateText: String {
        if model.estimating && model.estimate == nil { return "Counting messages…" }
        guard let e = model.estimate else { return "Connect Gmail to see how much older mail there is." }
        return "About \(e.total.formatted(.number.locale(locale))) messages" + (model.etaText.map { " · \($0)" } ?? "") + ". Runs in the background."
    }

    private func option(_ title: String, _ r: ImportOlderModel.Range, divider: Bool = true) -> some View {
        Button { model.range = r } label: {
            InsetRow(title, showsDivider: divider) {
                ZStack {
                    Circle().strokeBorder(model.range == r ? p.accent : p.hairlineStrong, lineWidth: 1.5)
                    if model.range == r { Circle().fill(p.accent).frame(width: 12, height: 12) }
                }.frame(width: 22, height: 22)
            }
        }.buttonStyle(.plain).accessibilityAddTraits(model.range == r ? .isSelected : [])
    }

    private var picker: some View {
        VStack(spacing: 0) {
            SheetHeader("Businesses", trailingTitle: "Done") { pickingBusinesses = false }
            ScrollView {
                InsetGroup {
                    Button { model.chosenBusinesses = [] } label: {
                        InsetRow("All businesses") { if model.chosenBusinesses.isEmpty { Image(systemName: "checkmark").foregroundStyle(p.accent) } }
                    }.buttonStyle(.plain)
                    ForEach(Array(model.businesses.enumerated()), id: \.element.id) { i, b in
                        Button { if model.chosenBusinesses.contains(b.id) { model.chosenBusinesses.remove(b.id) } else { model.chosenBusinesses.insert(b.id) } } label: {
                            InsetRow(b.name, subtitle: b.domain, showsDivider: i < model.businesses.count - 1) {
                                if model.chosenBusinesses.contains(b.id) { Image(systemName: "checkmark").foregroundStyle(p.accent) }
                            }
                        }.buttonStyle(.plain)
                    }
                }.padding(16)
            }
        }.background(p.ground)
    }

    // MARK: GM07 running, GM09 paused

    private var running: some View {
        let s = model.store
        return VStack(alignment: .leading, spacing: 18) {
            if s.isPaused {
                let t = model.pauseText()
                Banner(.warning, title: t.title, message: t.detail, systemImage: "pause.circle", inline: true)
            }
            VStack(alignment: .leading, spacing: 10) {
                Text("Importing \((s.days ?? s.rangeDays).map { $0 == 365 ? "the last year" : "the last \($0) days" } ?? "older mail")")
                    .font(.inboxHeadingBold(27, relativeTo: .title2)).tracking(-0.5).foregroundStyle(p.text)
                HStack {
                    Text("\(s.batchScanned.formatted(.number.locale(locale))) of \(s.batchEstimated.formatted(.number.locale(locale)))").font(.inboxBody(15)).foregroundStyle(p.secondary)
                    Spacer()
                    Text("\(Int(s.progress * 100))%").font(.inboxBody(15)).foregroundStyle(p.secondary)
                }
                InboxProgressBar(value: s.progress)
                Text("\(s.batchSorted.formatted(.number.locale(locale))) sorted · \(s.batchUnrouted.formatted(.number.locale(locale))) need a rule").font(.inboxBody(14)).foregroundStyle(p.secondary)
            }
            .accessibilityElement(children: .combine)
            InsetGroup("Businesses") {
                ForEach(Array(s.latestBatch.enumerated()), id: \.element.id) { i, b in
                    InsetRow(b.businessName, subtitle: statusText(b), showsDivider: i < s.latestBatch.count - 1) {
                        Text("\(b.scanned.formatted(.number.locale(locale))) / \(b.estimated.formatted(.number.locale(locale)))").font(.inboxBody(15)).foregroundStyle(p.secondary)
                    }
                }
            }
            Text("Runs in the background. You can use Inbox as usual.").font(.inboxBody(14)).foregroundStyle(p.secondary)
            if s.isPaused && s.pauseReason == "user" { PrimaryButton("Resume import") { Task { await model.resume() } } }
            else if s.isPaused && s.pauseReason == "gmail_signed_out" { EmptyView() }
            else if !s.isPaused { PrimaryButton("Pause import", style: .secondary) { Task { await model.pause() } } }
            Button { model.confirmingCancel = true } label: {
                Text("Cancel import").font(.inboxBody(16.5)).foregroundStyle(p.accentText).frame(maxWidth: .infinity, minHeight: 52).contentShape(Rectangle())
            }.buttonStyle(.plain)
        }
    }

    private func statusText(_ b: Backfill) -> String {
        switch b.status {
        case .queued: "Waiting"; case .running: "Importing"; case .paused: "Paused"; case .done: "Done"; case .failed: "Failed"; case .cancelled: "Cancelled"
        }
    }

    // MARK: GM08 done

    private var done: some View {
        let s = model.store
        return VStack(alignment: .leading, spacing: 18) {
            let failed = s.latestBatch.contains { $0.status == .failed }
            Banner(failed ? .warning : .ok, title: failed ? "Import finished with problems." : "Import finished.",
                   message: "\(s.batchImported.formatted(.number.locale(locale))) messages imported, \(s.batchSorted.formatted(.number.locale(locale))) sorted into your rules" + (s.batchUnrouted > 0 ? ", \(s.batchUnrouted.formatted(.number.locale(locale))) need a rule." : "."),
                   systemImage: failed ? "exclamationmark.triangle" : "checkmark.circle", inline: true)
            InsetGroup("Businesses") {
                ForEach(Array(s.latestBatch.enumerated()), id: \.element.id) { i, b in
                    InsetRow(b.businessName, subtitle: "\(b.imported.formatted(.number.locale(locale))) imported · \(b.unrouted.formatted(.number.locale(locale))) need a rule", showsDivider: i < s.latestBatch.count - 1)
                }
            }
            PrimaryButton("Import another range", style: .secondary) { showsDone = false }
        }
    }
}

/// First-open banner on Mail: "Importing the last 90 days · 1,240 of 3,180". Tap opens Settings → Import older mail.
public struct ImportProgressBanner: View {
    @Environment(\.palette) private var p
    @Environment(\.locale) private var locale
    let env: AppEnvironment
    let open: () -> Void
    public init(env: AppEnvironment, open: @escaping () -> Void) { self.env = env; self.open = open }
    /// "the last 90 days" / "the last year" / "older mail" when the range isn't known.
    static func rangeText(_ s: BackfillStore) -> String {
        guard let d = s.days ?? s.rangeDays else { return "older mail" }
        return d == 365 ? "the last year" : "the last \(d) days"
    }
    public var body: some View {
        let s = env.backfill
        if s.isActive {
            Button(action: open) {
                VStack(alignment: .leading, spacing: 8) {
                    HStack {
                        Text(s.isPaused ? "Import paused" : "Importing \(Self.rangeText(s))")
                            .font(.inboxBody(15, medium: true)).foregroundStyle(p.text)
                        Spacer()
                        Text("\(s.batchScanned.formatted(.number.locale(locale))) of \(s.batchEstimated.formatted(.number.locale(locale)))").font(.inboxBody(15)).foregroundStyle(p.secondary)
                    }
                    InboxProgressBar(value: s.progress)
                }
                .padding(.horizontal, 14).padding(.vertical, 12)
                .background(RoundedRectangle(cornerRadius: 12, style: .continuous).fill(p.card))
            }
            .buttonStyle(.plain)
            .accessibilityElement(children: .combine).accessibilityHint("Opens import settings")
        }
    }
}
