import SwiftUI
import DesignSystem
import Domain
import Persistence

/// Rule editor (RU05) with the invalid-pattern state (RU07) and the Gmail label picker sheet (RU06).
public struct RuleEditorScreen: View {
    @Environment(\.palette) private var p
    @Environment(\.dismiss) private var dismiss
    @State private var model: RuleEditorModel
    @State private var showLabels = false
    @Binding var path: [RuleRoute]
    private let env: AppEnvironment

    public init(env: AppEnvironment, draft: RuleDraft, path: Binding<[RuleRoute]>) {
        self.env = env; _path = path
        _model = State(initialValue: RuleEditorModel(env: env, draft: draft))
    }

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 24) {
                if let g = model.generalError { Banner(.error, title: g) }
                InsetGroup {
                    RuleFieldRow("Name", error: model.fieldErrors["name"], divider: false) {
                        TextField("Name", text: $model.draft.name)
                    }
                }
                InsetGroup("When mail matches") {
                    RuleFieldRow("Business") { businessMenu }
                    RuleFieldRow("To", error: model.fieldErrors["match_to_local"]) {
                        HStack(spacing: 0) {
                            TextField("Any address", text: $model.draft.toLocal)
                                .textInputAutocapitalization(.never).autocorrectionDisabled()
                            if !model.draft.toLocal.isEmpty { Text("@").foregroundStyle(p.secondary) }
                        }
                    }
                    RuleFieldRow("From", error: model.fieldErrors["match_from"]) {
                        TextField("Anyone", text: $model.draft.from).textInputAutocapitalization(.never).autocorrectionDisabled()
                    }
                    RuleFieldRow("Subject", error: model.subjectError(), divider: false) {
                        TextField("Any", text: $model.draft.subject).textInputAutocapitalization(.never).autocorrectionDisabled()
                            .foregroundStyle(model.subjectError() == nil ? p.text : p.destructive)
                    }
                }
                InsetGroup("Then in Gmail") {
                    Button { showLabels = true } label: {
                        RuleFieldRow("Add label") {
                            HStack {
                                Text(model.draft.label.isEmpty ? "No label" : model.draft.label)
                                    .foregroundStyle(model.draft.label.isEmpty ? p.tertiary : p.text)
                                Spacer()
                                Image(systemName: "chevron.right").font(.footnote.weight(.semibold)).foregroundStyle(p.tertiary)
                            }
                        }
                    }.buttonStyle(.plain)
                    RuleSwitchRow("Archive", isOn: $model.draft.archive)
                    RuleSwitchRow("Mark as read", isOn: $model.draft.markRead)
                    RuleSwitchRow("Label only", caption: "Don't archive or mark read, even if set", divider: false, isOn: $model.draft.labelOnly)
                }
                PrimaryButton("Test against recent mail", style: .secondary) { path.append(.test(model.draft)) }
                if let why = model.saveBlockedReason, model.draft.hasName || !model.isNew {
                    Text(why).font(.inboxBody(14, relativeTo: .subheadline)).foregroundStyle(p.destructive)
                }
                if !model.isNew {
                    PrimaryButton("Delete rule", style: .destructive) { Task { await delete() } }
                }
            }
            .padding(16).padding(.bottom, 24)
        }
        .scrollDismissesKeyboard(.interactively)
        .background(p.ground)
        .navigationTitle(model.title).inboxNavigationChrome()
        .toolbar {
            ToolbarItem(placement: .topBarLeading) { InboxToolbarButton("Cancel") { dismiss() } }.inboxFlat()
            ToolbarItem(placement: .topBarTrailing) {
                InboxToolbarButton("Save") { Task { if await model.save() { dismiss() } } }
                    .disabled(!model.canSave).opacity(model.canSave ? 1 : 0.4).accessibilityHint(model.saveBlockedReason ?? "")
            }.inboxFlat()
        }
        .sheet(isPresented: $showLabels) {
            LabelPickerSheet(provider: env.labels, selection: $model.draft.label)
                .presentationDetents([.large]).presentationDragIndicator(.hidden).environment(\.palette, p)
        }
        .onChange(of: model.draft) { model.edited() }
        .task { await model.load() }
    }

    private var businessMenu: some View {
        Menu {
            Button("Any business") { model.draft.businessId = nil }
            ForEach(model.businesses) { b in Button(b.name) { model.draft.businessId = b.id } }
        } label: {
            HStack {
                Text(model.businessName ?? "Any business").foregroundStyle(model.businessName == nil ? p.tertiary : p.text)
                Spacer()
                Image(systemName: "chevron.up.chevron.down").font(.footnote).foregroundStyle(p.tertiary)
            }
            .frame(minHeight: 44).contentShape(Rectangle())
        }
    }

    private func delete() async {
        guard let id = model.draft.id else { return }
        if (try? await env.rules.delete(id: id)) != nil { dismiss() }
    }
}
