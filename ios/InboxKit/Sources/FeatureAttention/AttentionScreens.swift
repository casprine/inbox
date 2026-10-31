import SwiftUI
import API
import DesignSystem
import Domain
import FeatureRules
import Persistence

/// Unrouted (NA02): plain mail lists grouped by business; tapping a message opens the rule-from-message sheet.
public struct UnroutedScreen: View {
    @Environment(\.palette) private var p
    @State private var model: UnroutedModel
    @State private var composing: API.Message?
    private let env: AppEnvironment
    public init(env: AppEnvironment) { self.env = env; _model = State(initialValue: UnroutedModel(env: env)) }

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                PageTitle("Unrouted", subtitle: "\(model.total) message\(model.total == 1 ? "" : "s") no rule matched")
                if model.failed { Banner(.warning, title: "Couldn't load", message: "Pull to refresh to try again.").padding(.horizontal, 16) }
                if model.isLoading { ProgressView().frame(maxWidth: .infinity).padding(.top, 40) }
                else if model.isEmpty { EmptyState(title: "Nothing unrouted", message: "Every message matched a rule or a business.") }
                else {
                    ForEach(Array(model.groups.enumerated()), id: \.offset) { _, g in
                        Text("\(model.businessName(g)) · \(g.count)".uppercased()).font(.inboxBody(12, medium: true)).tracking(0.6).monospacedDigit()
                            .foregroundStyle(p.secondary).padding(.horizontal, 20).padding(.top, 18).padding(.bottom, 6)
                        ForEach(g.messages) { m in
                            Button { composing = m } label: { MailRow(row(m)) }.buttonStyle(.plain)
                                .accessibilityHint("Make a rule from this message")
                        }
                    }
                    Text("These are in Gmail and in their business as usual. Make a rule so mail like this is sorted next time.")
                        .font(.inboxBody(13.5)).foregroundStyle(p.secondary).padding(.horizontal, 20).padding(.vertical, 16)
                }
            }
        }
        .background(p.ground)
        .inboxPushed(title: "", back: "Businesses")
        .refreshable { await model.load() }
        .task { await model.load() }
        .sheet(item: $composing, onDismiss: { Task { await model.load() } }) { m in
            RuleFromMessageSheet(env: env, message: m, businessName: m.businessId.flatMap { id in
                model.groups.first { $0.business?.id == id }?.business?.name }, unroutedTotal: model.total)
                .environment(\.palette, p)
        }
    }

    private func row(_ m: API.Message) -> MailRowModel {
        MailRowModel(id: m.id.uuidString, sender: m.fromName ?? m.fromAddr ?? "Unknown sender", subject: m.subject ?? "(no subject)",
                     preview: (m.snippet == m.subject ? "" : m.snippet) ?? "", time: RuleFormat.when(m.receivedAt), isUnread: !m.isRead)
    }
}

/// NA03: name, business, match, Gmail label, archive; shows how many unrouted messages it would catch.
public struct RuleFromMessageSheet: View {
    @Environment(\.palette) private var p
    @Environment(\.dismiss) private var dismiss
    @State private var model: RuleFromMessageModel
    @State private var showLabels = false
    private let env: AppEnvironment

    public init(env: AppEnvironment, message: API.Message, businessName: String?, unroutedTotal: Int) {
        self.env = env
        _model = State(initialValue: RuleFromMessageModel(env: env, message: message, businessName: businessName, unroutedTotal: unroutedTotal))
    }

    public var body: some View {
        VStack(spacing: 0) {
            SheetHeader("Rule from this message", trailingTitle: "Cancel") { dismiss() }
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    InsetGroup {
                        RuleFieldRow("Name") { TextField("Name", text: $model.draft.name) }
                        RuleFieldRow("Business") { Text(businessText).foregroundStyle(p.text) }
                        RuleFieldRow("Match", error: RuleEditorModelPattern.problem(model.draft.subject) == nil ? nil : "Pattern isn't valid.") {
                            if let d = model.draft.subjectDescription { Text(d) }
                            else if !model.draft.from.isEmpty { Text("From \(model.draft.from)") }
                            else { Text("Any mail").foregroundStyle(p.tertiary) }
                        }
                        Button { showLabels = true } label: {
                            RuleFieldRow("Label in Gmail") {
                                Text(model.draft.label.isEmpty ? "No label" : model.draft.label)
                                    .foregroundStyle(model.draft.label.isEmpty ? p.tertiary : p.text)
                            }
                        }.buttonStyle(.plain)
                        RuleSwitchRow("Archive in Gmail", divider: false, isOn: Binding(
                            get: { model.draft.archive },
                            set: { model.draft.archive = $0; if $0 { model.draft.labelOnly = false } }))
                    }
                    Text(model.matchSummary).font(.inboxBody(15)).foregroundStyle(p.secondary)
                    if let e = model.errorText { Text(e).font(.inboxBody(14)).foregroundStyle(p.accentText) }
                    PrimaryButton("Create rule") { Task { if await model.create() { dismiss() } } }
                        .disabled(!model.canCreate).opacity(model.canCreate ? 1 : 0.5)
                }
                .padding(16)
            }
        }
        .background(p.ground)
        .presentationDetents([.large]).presentationDragIndicator(.hidden)
        .sheet(isPresented: $showLabels) {
            LabelPickerSheet(provider: env.labels, selection: $model.draft.label).environment(\.palette, p)
        }
        .task { await model.load() }
        .task(id: model.draft) { try? await Task.sleep(for: .milliseconds(300)); await model.recount() }
    }

    private var businessText: String {
        model.draft.businessId.flatMap { model.businessNames[$0] } ?? "Any business"
    }
}

/// Failed (NA04): plain mail list of what couldn't be stored; a row shows "Retrying…" while a retry runs (NA06), empty state NA07.
public struct FailedScreen: View {
    @Environment(\.palette) private var p
    @State private var model: FailedModel
    @Binding var path: [Route]
    private let previewRetrying: Bool

    public init(env: AppEnvironment, path: Binding<[Route]>, previewRetrying: Bool = false) {
        _model = State(initialValue: FailedModel(env: env)); _path = path; self.previewRetrying = previewRetrying
    }

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                PageTitle("Failed", subtitle: model.isEmpty || model.isLoading ? nil : "\(model.items.count) message\(model.items.count == 1 ? "" : "s") couldn't be processed")
                if let n = model.notice { Banner(.warning, title: n).padding(.horizontal, 16) }
                if model.failed { Banner(.warning, title: "Couldn't load", message: "Pull to refresh to try again.").padding(.horizontal, 16) }
                if model.isLoading { ProgressView().frame(maxWidth: .infinity).padding(.top, 40) }
                else if model.isEmpty { EmptyState(title: "Nothing failed", message: "Every message has been stored and sorted.") }
                else {
                    ForEach(model.groups) { g in
                        Text("\(g.name) · \(g.items.count)".uppercased()).font(.inboxBody(12, medium: true)).tracking(0.6).monospacedDigit()
                            .foregroundStyle(p.secondary).padding(.horizontal, 20).padding(.top, 18).padding(.bottom, 6)
                            .accessibilityAddTraits(.isHeader)
                        ForEach(g.items) { f in
                            Button { path.append(.failedDetail(f.id)) } label: { MailRow(row(f)) }.buttonStyle(.plain)
                        }
                    }
                    Text("These were delivered to Gmail, but Inbox couldn't process them.")
                        .font(.inboxBody(13.5)).foregroundStyle(p.secondary).padding(.horizontal, 20).padding(.vertical, 16)
                }
            }
        }
        .background(p.ground)
        .inboxPushed(title: "", back: "Businesses")
        .refreshable { await model.load() }
        .task { await model.load(); if previewRetrying, let f = model.items.first { model.previewRetrying(f.id) } }
    }

    private func row(_ f: FailedMessage) -> MailRowModel {
        let copy = FailureCopy.text(reason: f.failureReason, sizeBytes: f.sizeBytes)
        return MailRowModel(id: f.id.uuidString, sender: f.message.fromName ?? f.message.fromAddr ?? "Unknown sender",
                            subject: f.message.subject ?? "(no subject)", preview: model.retrying.contains(f.id) ? "Retrying…" : copy.lead,
                            time: RuleFormat.when(f.message.receivedAt), isUnread: false)
    }
}

/// Failed detail (NA05): alert banner with the reason, message info, a plain-words log, then inset action rows.
public struct FailedDetailScreen: View {
    @Environment(\.palette) private var p
    @Environment(\.dismiss) private var dismiss
    @Environment(\.openURL) private var openURL
    @State private var model: FailedModel
    private let id: UUID

    public init(env: AppEnvironment, id: UUID) { self.id = id; _model = State(initialValue: FailedModel(env: env)) }

    private var item: FailedMessage? { model.items.first { $0.id == id } }

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                if let f = item { detail(f) }
                else if model.isLoading { ProgressView().frame(maxWidth: .infinity).padding(.top, 40) }
                else { EmptyState(title: "Already handled", message: "This message is no longer in Failed.") }
            }
            .padding(16)
        }
        .background(p.ground)
        .inboxPushed(title: "Failed", back: "Failed")
        .task { await model.load() }
    }

    @ViewBuilder private func detail(_ f: FailedMessage) -> some View {
        let copy = FailureCopy.text(reason: f.failureReason, sizeBytes: f.sizeBytes)
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: "exclamationmark.circle.fill").font(.system(size: 18)).foregroundStyle(p.accent).padding(.top, 1)
            (Text(copy.lead + " ").font(.inboxBody(15, medium: true)).foregroundColor(p.text)
                + Text(copy.detail).font(.inboxBody(15)).foregroundColor(p.text))
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 14).padding(.vertical, 12)
        .background(RoundedRectangle(cornerRadius: 12, style: .continuous).fill(p.card))
        .accessibilityElement(children: .combine)
        if let n = model.notice { Banner(.warning, title: n) }
        InsetGroup("Message") {
            info("From", f.message.fromName ?? f.message.fromAddr ?? "Unknown")
            info("To", f.envTo ?? f.message.toLocal.map { "\($0)@" } ?? "Unknown")
            info("Received", f.message.receivedAt.formatted(.dateTime.month(.abbreviated).day().hour().minute()), divider: false)
        }
        InsetGroup("What happened") {
            Text(log(f)).font(.inboxMono(12)).foregroundStyle(p.text).frame(maxWidth: .infinity, alignment: .leading).padding(16)
        }
        InsetGroup {
            action("Open in Gmail", divider: f.failureReason != "parse_failed") { openURL(GmailLink.web) }
            if f.failureReason != "parse_failed" {
                action(model.retrying.contains(f.id) ? "Retrying…" : "Retry", divider: false) { Task { await model.retry(f.id) } }
            }
        }
        InsetGroup { action("Dismiss", color: p.accentText, divider: false) { Task { if await model.dismiss(f.id) { dismiss() } } } }
    }

    private func action(_ title: String, color: Color? = nil, divider: Bool, _ run: @escaping () -> Void) -> some View {
        Button(action: run) {
            Text(title).font(.inboxBody(16.5)).foregroundStyle(color ?? p.accentText).frame(maxWidth: .infinity, minHeight: 52).contentShape(Rectangle())
                .overlay(alignment: .bottom) { if divider { Rectangle().fill(p.hairline).frame(height: 1) } }
        }.buttonStyle(.plain)
    }

    private func info(_ k: String, _ v: String, divider: Bool = true) -> some View {
        RuleFieldRow(k, divider: divider) { Text(v) }
    }

    private func log(_ f: FailedMessage) -> String {
        let t = f.message.receivedAt.formatted(.dateTime.hour(.twoDigits(amPM: .omitted)).minute(.twoDigits).second(.twoDigits))
        let size = f.sizeBytes.map { String(format: "%.1f MB", Double($0) / 1_048_576) } ?? "message"
        let why: String
        switch f.failureReason {
        case "parse_failed": why = "parse → failed (malformed MIME)"
        case "too_large": why = "store raw → rejected (size)"
        default: why = "store raw → failed (storage unavailable)"
        }
        return "\(t) received \(size)\n\(t) forwarded to Gmail ✓\n\(t) \(why)\n\(t) marked failed"
    }
}

enum GmailLink { static let web = URL(string: "https://mail.google.com/mail/u/0/#all")! }
