import SwiftUI
import DesignSystem
import Domain
import API
import Persistence

/// Full-screen search (SR01 idle, SR02 results). Pushed on the Mail stack via `Route.search`; Cancel pops it.
public struct SearchScreen: View {
    @Environment(\.palette) private var p
    @Environment(\.dismiss) private var dismiss
    @State private var model: SearchModel
    @FocusState private var focused: Bool
    @Binding var path: [Route]
    let initialQuery: String?

    public init(env: AppEnvironment, scope: UUID?, importing: Bool = false, useMockData: Bool = false,
                initialQuery: String? = nil, path: Binding<[Route]>) {
        let backend: any SearchBackend = useMockData ? LocalSearchBackend(db: env.database) : APISearchBackend(api: env.api, db: env.database)
        _model = State(initialValue: SearchModel(backend: backend, scope: scope, importing: importing))
        self.initialQuery = initialQuery; _path = path; db = env.database
    }
    private let db: AppDatabase

    public var body: some View {
        VStack(spacing: 0) {
            bar
            if model.showsScopeControl { scopeControl }
            content
        }
        .background(p.ground)
        .toolbar(.hidden, for: .navigationBar)
        .task {
            if let name = try? await db.businesses().first(where: { $0.id == model.scope })?.name { model.setScopeName(name) }
            await model.loadRecents()
            if let q = initialQuery, !q.isEmpty { model.query = q; await model.submit() } else { focused = true }
        }
    }

    // MARK: Top bar

    private var bar: some View {
        HStack(spacing: 12) {
            HStack(spacing: 8) {
                Image(systemName: "magnifyingglass").font(.inboxBody(16)).foregroundStyle(p.secondary)
                TextField("", text: $model.query,
                          prompt: Text(model.showsScopeChip ? "Search \(model.scopeName ?? "this business")" : "Search all businesses").foregroundColor(p.textTertiary))
                    .font(.inboxBody(17)).foregroundStyle(p.text).tint(p.accent)
                    .focused($focused).submitLabel(.search)
                    .textInputAutocapitalization(.never).autocorrectionDisabled()
                    .onSubmit { Task { await model.submit() } }
                    .accessibilityIdentifier("search.field")
                if !model.query.isEmpty {
                    Button { model.query = ""; focused = true } label: { Image(systemName: "xmark.circle.fill").foregroundStyle(p.hairlineStrong) }
                        .accessibilityLabel("Clear text")
                }
            }
            .padding(.horizontal, 10).frame(minHeight: 38)
            .background(RoundedRectangle(cornerRadius: 10, style: .continuous).fill(p.field))
            .overlay { if focused { RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(p.accent, lineWidth: 1.5) } }
            Button("Cancel") { dismiss() }.font(.inboxBody(17)).foregroundStyle(p.accentText).buttonStyle(.plain).frame(minHeight: 44)
        }
        .padding(.horizontal, 16).padding(.vertical, 8)
    }

    /// "All businesses | <business>" under the field whenever search was opened from a business.
    private var scopeControl: some View {
        HStack(spacing: 2) {
            seg("All businesses", on: !model.showsScopeChip) { model.widenScope() }
            seg(model.contextName ?? "This business", on: model.showsScopeChip) { model.narrowScope() }
        }
        .padding(2).background(RoundedRectangle(cornerRadius: 9, style: .continuous).fill(p.field))
        .padding(.horizontal, 16).padding(.bottom, 8)
        .accessibilityElement(children: .contain)
    }

    private func seg(_ title: String, on: Bool, _ action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Text(title).font(.inboxBody(14, medium: on)).foregroundStyle(p.text).lineLimit(1)
                .frame(maxWidth: .infinity, minHeight: 32)
                .background { if on { RoundedRectangle(cornerRadius: 7, style: .continuous).fill(p.card).shadow(color: .black.opacity(0.08), radius: 1.5, y: 1) } }
        }
        .buttonStyle(.plain).accessibilityAddTraits(on ? .isSelected : [])
    }

    // MARK: Content

    @ViewBuilder private var content: some View {
        switch model.phase {
        case .idle: idle
        case .results: results
        case .none: none
        case .failed:
            VStack { Spacer(); EmptyState(title: "Search failed", message: "Check your connection and try again.", actionTitle: "Try again") { model.retry() }; Spacer() }
        }
    }

    private var idle: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                if !model.recents.isEmpty {
                    VStack(alignment: .leading, spacing: 8) {
                        HStack {
                            header("Recent")
                            Spacer()
                            Button("Clear") { Task { await model.clearRecents() } }
                                .font(.inboxBody(15)).foregroundStyle(p.accentText).buttonStyle(.plain).frame(minHeight: 44)
                        }
                        .padding(.horizontal, 16)
                        VStack(spacing: 0) {
                            ForEach(Array(model.recents.enumerated()), id: \.element) { i, q in
                                Button { Task { await model.useRecent(q) } } label: {
                                    HStack(spacing: 12) {
                                        Image(systemName: "clock").font(.system(size: 20)).foregroundStyle(p.accent).frame(width: 24)
                                        Text(q).font(.inboxBody(16.5)).foregroundStyle(p.text)
                                        Spacer()
                                    }
                                    .padding(.horizontal, 16).frame(minHeight: 56).contentShape(Rectangle())
                                    .overlay(alignment: .bottom) { if i < model.recents.count - 1 { Rectangle().fill(p.hairline).frame(height: 1).padding(.leading, 52) } }
                                }
                                .buttonStyle(.plain)
                                .contextMenu { Button("Remove", role: .destructive) { Task { await model.removeRecent(q) } } }
                                .accessibilityAction(named: "Remove") { Task { await model.removeRecent(q) } }
                            }
                        }
                        .background(RoundedRectangle(cornerRadius: 12, style: .continuous).fill(p.card))
                    }
                }
                VStack(alignment: .leading, spacing: 8) {
                    header("Try").padding(.horizontal, 16)
                    VStack(spacing: 0) {
                        ForEach(Array(SearchOperator.allCases.enumerated()), id: \.element) { i, op in
                            Button { model.insert(op); focused = true } label: {
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(op.rawValue).font(.inboxBody(16.5)).foregroundStyle(p.text)
                                    if op != .hasAttachment { Text(op.hint).font(.inboxBody(14)).foregroundStyle(p.secondary) }
                                }
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .padding(.horizontal, 16).frame(minHeight: 56).contentShape(Rectangle())
                                .overlay(alignment: .bottom) { if i < SearchOperator.allCases.count - 1 { Rectangle().fill(p.hairline).frame(height: 1).padding(.leading, 16) } }
                            }
                            .buttonStyle(.plain)
                        }
                    }
                    .background(RoundedRectangle(cornerRadius: 12, style: .continuous).fill(p.card))
                }
            }
            .padding(16)
        }
        .scrollDismissesKeyboard(.interactively)
    }

    private var results: some View {
        List {
            ForEach(model.sections) { section in
                Section {
                    ForEach(section.hits) { hit in
                        Button {
                            Task { await model.opened() }
                            path.append(.message(hit.id))
                        } label: { SearchRow(hit: hit, terms: model.terms) }
                            .buttonStyle(.plain).plainRow()
                    }
                } header: {
                    HStack(spacing: 8) {
                        if !model.showsScopeChip || model.sections.count > 1 { header(section.name) }
                        else { header(section.name) }
                        Text("\(section.total)").font(.inboxBody(12.5, medium: true)).foregroundStyle(p.secondary)
                        Spacer()
                    }
                    .padding(.horizontal, 16).padding(.vertical, 6).frame(maxWidth: .infinity).background(p.ground)
                }
                .listSectionSeparator(.hidden)
            }
            if model.canLoadMore {
                HStack {
                    Spacer()
                    if model.loadMoreFailed {
                        Button("Couldn't load more. Try again") { Task { await model.loadMore() } }
                            .font(.inboxBody(15)).foregroundStyle(p.accentText).buttonStyle(.plain).frame(minHeight: 44)
                    } else { ProgressView().accessibilityLabel("Loading more results") }
                    Spacer()
                }
                .listRowSeparator(.hidden).listRowBackground(Color.clear)
                .onAppear { Task { await model.loadMore() } }   // the end of the list scrolled into view
            }
        }
        .listStyle(.plain).scrollContentBackground(.hidden).scrollDismissesKeyboard(.interactively)
        .accessibilityIdentifier("search.results")
    }

    private var none: some View {
        ScrollView {
            VStack(spacing: 16) {
                EmptyState(title: "No results", message: "Nothing matches “\(model.trimmed)”\(model.showsScopeChip ? " in \(model.scopeName ?? "this business")" : "").")
                if model.showsScopeChip {
                    PrimaryButton("Search all businesses", style: .secondary) { model.widenScope() }
                }
                if model.importing {
                    Banner(.idle, title: "Still importing older mail", message: "Results may be incomplete until the import finishes.")
                        .padding(.horizontal, 16)
                }
            }
            .padding(.top, 40)
        }
        .scrollDismissesKeyboard(.interactively)
    }

    private func header(_ s: String) -> some View { Text(s.uppercased()).font(.inboxBody(12.5, medium: true)).tracking(0.5).foregroundStyle(p.secondary) }
}

private extension View {
    func plainRow() -> some View {
        listRowInsets(EdgeInsets(top: 10, leading: 16, bottom: 10, trailing: 16)).listRowSeparator(.hidden).listRowBackground(Color.clear)
    }
}

struct SearchRow: View {
    @Environment(\.palette) private var p
    let hit: SearchHit
    let terms: [String]

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack {
                text(hit.sender, hit.senderSpans).font(.inboxBody(16, medium: !hit.isRead)).foregroundStyle(p.text).lineLimit(1)
                Spacer(minLength: 8)
                Text(Self.time(hit.receivedAt)).font(.inboxBody(14, relativeTo: .subheadline)).foregroundStyle(p.secondary)
            }
            text(hit.subject, hit.subjectSpans).font(.inboxBody(15)).foregroundStyle(p.text).lineLimit(1)
            if !hit.snippet.isEmpty { text(hit.snippet, hit.snippetSpans).font(.inboxBody(15)).foregroundStyle(p.secondary).lineLimit(2) }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .contentShape(Rectangle())
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("\(hit.isRead ? "" : "Unread. ")\(hit.sender). \(hit.subject). \(Self.time(hit.receivedAt))")
    }

    /// Server offsets win; when it sent none (or in mock mode) fall back to matching the query terms here.
    private func text(_ s: String, _ spans: [Span]) -> Text {
        let use = spans.isEmpty ? SearchHighlight.spans(in: s, terms: terms) : spans
        var out = AttributedString()
        for seg in SearchHighlight.segments(s, spans: use) {
            var a = AttributedString(seg.text)
            if seg.highlighted { a.backgroundColor = p.highlight }
            out.append(a)
        }
        return Text(out)
    }

    static func time(_ d: Date, now: Date = .now) -> String {
        let cal = Calendar.current
        if cal.isDate(d, inSameDayAs: now) { return d.formatted(date: .omitted, time: .shortened) }
        if cal.isDateInYesterday(d) { return "Yesterday" }
        let days = cal.dateComponents([.day], from: cal.startOfDay(for: d), to: cal.startOfDay(for: now)).day ?? 99
        return days < 7 ? d.formatted(.dateTime.weekday(.abbreviated)) : d.formatted(.dateTime.month(.abbreviated).day())
    }
}
