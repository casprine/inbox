import SwiftUI
import Observation
import API
import DesignSystem
import Domain

@MainActor @Observable
public final class ImportFiltersModel {
    public enum Phase { case loading, loaded, failed(String), done(Int) }
    public private(set) var phase: Phase = .loading
    public private(set) var filters: [GmailFilter] = []
    public var selected: Set<String> = []
    public private(set) var isImporting = false
    private let env: AppEnvironment
    public init(env: AppEnvironment) { self.env = env }

    public var importable: [GmailFilter] { filters.filter { $0.importable && !$0.alreadyImported } }
    public var blocked: [GmailFilter] { filters.filter { !$0.importable } }
    public static func reasonText(_ r: String?) -> String {
        switch r {
        case "deletes_mail": "Inbox never deletes mail"
        case "forwards_mail": "Inbox doesn't forward mail"
        case "no_label": "No label to apply"
        case "unmanaged_domain": "Not one of your businesses"
        default: "Can't be turned into a rule"
        }
    }

    public func load() async {
        phase = .loading
        do {
            filters = try await env.api.gmailFilters()
            selected = Set(importable.map(\.id)); phase = .loaded
        } catch let e as APIError {
            if case .problem(let p, _) = e, p.code == "gmail_not_connected" { phase = .failed("Connect Gmail in Settings to import its filters.") }
            else if case .problem(let p, _) = e, p.code == "gmail_needs_reauth" { phase = .failed("Reconnect Gmail in Settings to import its filters.") }
            else if case .http(let s) = e, s == 404 || s == 501 { phase = .failed("Importing filters isn't available on this server yet.") }
            else { phase = .failed("Couldn't load your Gmail filters.") }
        } catch { phase = .failed("Couldn't load your Gmail filters.") }
    }

    public func toggle(_ id: String) { if selected.contains(id) { selected.remove(id) } else { selected.insert(id) } }

    /// Imports the ticked filters as label-only rules and refreshes the rules list.
    public func run() async {
        guard !selected.isEmpty, !isImporting else { return }
        isImporting = true; defer { isImporting = false }
        do {
            let res = try await env.api.importFilters(ids: importable.map(\.id).filter(selected.contains))
            try? await env.rules.refresh()
            phase = .done(res.imported.count)
        } catch { phase = .failed("Couldn't import. Nothing was changed.") }
    }
}

/// The list part of RU04 (also OB08): filters to import, filters that can't be imported, and the done/failed states.
public struct ImportFiltersContent: View {
    @Environment(\.palette) private var p
    @Bindable var model: ImportFiltersModel
    public init(model: ImportFiltersModel) { self.model = model }

    public var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            switch model.phase {
            case .loading: ProgressView().frame(maxWidth: .infinity).padding(.top, 40)
            case .failed(let m): Banner(.warning, title: m, actionTitle: "Retry", systemImage: "exclamationmark.triangle") { Task { await model.load() } }
            case .done(let n): EmptyState(title: "Imported \(n) rule\(n == 1 ? "" : "s")", message: "They start label-only. Open a rule to turn on archive.")
            case .loaded: loaded
            }
        }
    }

    @ViewBuilder private var loaded: some View {
        InsetGroup("\(model.importable.count) filter\(model.importable.count == 1 ? "" : "s") found") {
            ForEach(Array(model.importable.enumerated()), id: \.element.id) { i, f in
                Button { model.toggle(f.id) } label: {
                    HStack(spacing: 12) {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(f.title).font(.inboxBody(16.5)).foregroundStyle(p.text)
                            Text(f.summary).font(.inboxBody(14)).foregroundStyle(p.secondary)
                        }
                        Spacer()
                        ZStack {
                            Circle().strokeBorder(p.hairlineStrong, lineWidth: 1.5).opacity(model.selected.contains(f.id) ? 0 : 1)
                            if model.selected.contains(f.id) {
                                Circle().fill(p.accentFill)
                                Image(systemName: "checkmark").font(.system(size: 12, weight: .bold)).foregroundStyle(p.onAccent)
                            }
                        }
                        .frame(width: 22, height: 22)
                    }
                    .padding(.horizontal, 16).frame(minHeight: 56).contentShape(Rectangle())
                    .overlay(alignment: .bottom) { if i < model.importable.count - 1 { Rectangle().fill(p.hairline).frame(height: 1).padding(.leading, 16) } }
                }
                .buttonStyle(.plain).accessibilityAddTraits(model.selected.contains(f.id) ? .isSelected : [])
            }
        }
        if !model.blocked.isEmpty {
            InsetGroup("Can't import") {
                ForEach(Array(model.blocked.enumerated()), id: \.element.id) { i, f in
                    VStack(alignment: .leading, spacing: 2) {
                        Text((f.summary.isEmpty ? f.title : f.summary) + (f.reason == "deletes_mail" ? " → delete" : "")).font(.inboxBody(16)).foregroundStyle(p.secondary)
                        Text(ImportFiltersModel.reasonText(f.reason)).font(.inboxBody(14)).foregroundStyle(p.secondary)
                    }
                    .padding(.horizontal, 16).padding(.vertical, 10).frame(maxWidth: .infinity, alignment: .leading)
                    .overlay(alignment: .bottom) { if i < model.blocked.count - 1 { Rectangle().fill(p.hairline).frame(height: 1).padding(.leading, 16) } }
                }
            }
        }
    }
}

/// RU04: Gmail filters that can become rules (label-only); filters that delete mail can't. Import sits in the nav bar.
public struct ImportFiltersScreen: View {
    @Environment(\.palette) private var p
    @Environment(\.dismiss) private var dismiss
    @State private var model: ImportFiltersModel
    public init(env: AppEnvironment) { _model = State(initialValue: ImportFiltersModel(env: env)) }

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                if case .loaded = model.phase {
                    Text("Imported rules start label-only. Turn on archive per rule once you've deleted the matching Gmail filter.")
                        .font(.inboxBody(15)).foregroundStyle(p.secondary)
                }
                ImportFiltersContent(model: model)
                if case .done = model.phase { PrimaryButton("Done") { dismiss() } }
            }
            .padding(16)
        }
        .background(p.ground)
        .inboxPushed(title: "Import from Gmail", back: "Rules")
        .toolbar {
            if case .loaded = model.phase {
                ToolbarItem(placement: .topBarTrailing) {
                    InboxToolbarButton(model.isImporting ? "Importing…" : "Import \(model.selected.count)") { Task { await model.run() } }
                        .disabled(model.selected.isEmpty || model.isImporting).opacity(model.selected.isEmpty ? 0.4 : 1)
                }.inboxFlat()
            }
        }
        .task { await model.load() }
    }
}
