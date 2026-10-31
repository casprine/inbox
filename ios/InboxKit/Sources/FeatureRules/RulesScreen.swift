import SwiftUI
import DesignSystem
import Domain
import Persistence

/// Rules tab: list with switches (RU01), Edit mode for reorder + delete (RU02), empty state (RU03), delete alert (RU10).
public struct RulesScreen: View {
    @Environment(\.palette) private var p
    @State private var model: RulesListModel
    @State private var editMode: EditMode = .inactive
    @Environment(\.dynamicTypeSize) private var typeSize
    @State private var path: [RuleRoute] = []
    private let env: AppEnvironment
    private let live: Int
    private let importScreen: AnyView

    /// `live` is a tick that triggers a refetch (SSE); pass 0 when offline.
    public init<Import: View>(env: AppEnvironment, live: Int = 0, @ViewBuilder importScreen: () -> Import) {
        self.env = env; self.live = live; self.importScreen = AnyView(importScreen())
        _model = State(initialValue: RulesListModel(env: env))
    }

    public var body: some View {
        NavigationStack(path: $path) {
            content
                .background(p.ground)
                .inboxNavigationChrome()
                .toolbar { toolbar }
                .navigationDestination(for: RuleRoute.self) { route in
                    switch route {
                    case .editor(let draft): RuleEditorScreen(env: env, draft: draft, path: $path)
                    case .test(let draft): RuleTestScreen(env: env, draft: draft)
                    case .importFilters: importScreen
                    }
                }
        }
        .environment(\.editMode, $editMode)
        .alert("Delete this rule?", isPresented: Binding(get: { model.pendingDelete != nil }, set: { if !$0 { model.pendingDelete = nil } }),
               presenting: model.pendingDelete) { r in
            Button("Delete rule", role: .destructive) { Task { await model.confirmDelete() } }
            Button("Cancel", role: .cancel) {}
        } message: { r in
            Text("“\(r.name)” stops sorting mail. Mail it already moved stays where it is.")
        }
        .task { await model.observe() }
        .task { await model.refresh(); if UserDefaults.standard.bool(forKey: "OpenImport"), path.isEmpty { path = [.importFilters] } }
        .task(id: live) { if live > 0 { await model.refresh() } }
    }

    @ViewBuilder private var content: some View {
        if model.isLoading {
            List {
                PageTitle("Rules").plain()
                ForEach(0..<5, id: \.self) { _ in skeletonRow }
            }.listStyle(.plain).scrollContentBackground(.hidden)
        } else if model.isEmpty {
            VStack(spacing: 16) {
                Spacer()
                EmptyState(title: "No rules yet", message: "Rules send mail to a Gmail label as it arrives. Start from a message, or write one here.",
                           actionTitle: "New rule") { path.append(.editor(RuleDraft())) }
                PrimaryButton("Import from Gmail filters", style: .plain) { path.append(.importFilters) }
                Spacer()
            }
        } else if editMode == .active {
            // RU02: List only here, for drag-to-reorder and swipe-to-delete.
            List {
                PageTitle("Rules").plain()
                Section {
                    editAction("New rule") { path.append(.editor(RuleDraft())) }
                    editAction("Import from Gmail filters") { path.append(.importFilters) }
                }
                .listRowBackground(p.card)
                Section {
                    ForEach(model.rules) { r in row(r) }
                        .onMove { from, to in Task { await model.move(from: from, to: to) } }
                        .onDelete { offsets in model.pendingDelete = offsets.first.map { model.rules[$0] } }
                } footer: {
                    Text("The first rule that matches wins. Drag to change priority.").font(.inboxBody(13.5)).foregroundStyle(p.secondary)
                }
                .listRowBackground(p.card)
            }
            .listStyle(.insetGrouped).scrollContentBackground(.hidden)
        } else {
            // RU01: title, search field and one inset card at the board's 16/20 pt metrics.
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    PageTitle("Rules")
                    VStack(alignment: .leading, spacing: 12) {
                        if let n = model.notice { Banner(.warning, title: n) }
                        if model.refreshFailed { Banner(.warning, title: "Couldn't refresh", message: "Showing what's saved on this phone.") }
                        searchField
                        InsetGroup {
                            ForEach(Array(model.filtered.enumerated()), id: \.element.id) { i, r in
                                row(r).padding(.horizontal, 16)
                                    .overlay(alignment: .bottom) { if i < model.filtered.count - 1 { Rectangle().fill(p.hairline).frame(height: 1).padding(.leading, 16) } }
                            }
                        }
                        if model.filtered.isEmpty { Text("No rules match “\(model.query)”.").font(.inboxBody(15)).foregroundStyle(p.secondary).padding(.horizontal, 20) }
                    }
                    .padding(.horizontal, 16).padding(.bottom, 24)
                }
            }
            .refreshable { await model.refresh() }
        }
    }

    private var searchField: some View {
        HStack(spacing: 8) {
            Image(systemName: "magnifyingglass").font(.inboxBody(16)).foregroundStyle(p.secondary)
            TextField("", text: $model.query, prompt: Text("Search rules").foregroundColor(p.textTertiary))
                .font(.inboxBody(17)).foregroundStyle(p.text).tint(p.accent).textInputAutocapitalization(.never).autocorrectionDisabled()
            if !model.query.isEmpty {
                Button { model.query = "" } label: { Image(systemName: "xmark.circle.fill").foregroundStyle(p.hairlineStrong) }.accessibilityLabel("Clear text")
            }
        }
        .padding(.horizontal, 10).frame(minHeight: 38)
        .background(RoundedRectangle(cornerRadius: 10, style: .continuous).fill(p.field))
        .frame(maxWidth: .infinity)
    }

    private func editAction(_ title: String, _ action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Text(title).font(.inboxBody(16.5)).foregroundStyle(p.accentText).frame(maxWidth: .infinity, minHeight: 44, alignment: .leading).contentShape(Rectangle())
        }.buttonStyle(.plain)
    }

    private var skeletonRow: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("Rule name").font(.inboxBody(17)); Text("to@ on domain").font(.inboxBody(14))
        }
        .redacted(reason: .placeholder).padding(16).frame(maxWidth: .infinity, alignment: .leading).plain()
    }

    private func row(_ r: RuleRecord) -> some View {
        HStack(spacing: 12) {
            Button { path.append(.editor(RuleDraft(rule: r))) } label: {
                VStack(alignment: .leading, spacing: 2) {
                    Text(r.name).font(.inboxBody(16.5)).foregroundStyle(p.text)
                    Text(model.summary(r)).font(.inboxBody(14, relativeTo: .subheadline)).foregroundStyle(p.secondary).lineLimit(typeSize.isAccessibilitySize ? nil : 2)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            if editMode == .inactive {
                SquareSwitch(isOn: Binding(get: { r.enabled }, set: { v in Task { await model.setEnabled(v, for: r) } }))
                    .accessibilityLabel("\(r.name) enabled")
            }
        }
        .padding(.vertical, 9).frame(minHeight: 60)
        .accessibilityElement(children: .contain)
    }

    @ToolbarContentBuilder private var toolbar: some ToolbarContent {
        if !model.rules.isEmpty {
            ToolbarItem(placement: .topBarTrailing) {
                InboxToolbarButton(editMode == .active ? "Done" : "Edit") { withAnimation { editMode = editMode == .active ? .inactive : .active } }
            }.inboxFlat()
        } else {
            ToolbarItem(placement: .topBarTrailing) {
                InboxToolbarButton("New") { path.append(.editor(RuleDraft())) }
            }.inboxFlat()
        }
    }
}

public enum RuleRoute: Hashable {
    case editor(RuleDraft)
    case test(RuleDraft)
    case importFilters
}

extension View {
    fileprivate func plain() -> some View { listRowInsets(EdgeInsets(top: 0, leading: 0, bottom: 0, trailing: 0)).listRowSeparator(.hidden).listRowBackground(Color.clear) }
}

