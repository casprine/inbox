import SwiftUI
import DesignSystem
import Domain
import Persistence

/// Shared list UI for the Business page and All businesses: rows, swipe, select mode, bulk bar, toast.
struct MessageListScreen<Header: View>: View {
    @Environment(\.palette) private var p
    @Bindable var model: MessageListModel
    let live: LiveEvents
    @Binding var path: [Route]
    let searchScope: UUID?
    var backTitle = "Businesses"
    @ViewBuilder var header: Header

    var body: some View {
        List {
            header
                .listRowInsets(EdgeInsets()).listRowSeparator(.hidden).listRowBackground(Color.clear)
            if model.refreshFailed && !model.messages.isEmpty {
                Banner(.warning, title: "Couldn't refresh", message: "Showing what's saved on this phone.")
                    .listRowInsets(EdgeInsets(top: 0, leading: 16, bottom: 8, trailing: 16))
                    .listRowSeparator(.hidden).listRowBackground(Color.clear)
            }
            if model.isLoading {
                ForEach(0..<8, id: \.self) { _ in MailRow(nil).plainRow() }
            } else if model.isEmpty {
                EmptyState(title: model.emptyKind == .allRead ? "All read" : "No mail yet",
                           message: model.emptyKind == .allRead ? "Nothing unread here." : "New mail for this address will show up here.")
                    .plainRow()
            } else {
                let sections = model.sections   // once per update: each is a filter over every message
                let lastID = sections.last?.messages.last?.id
                ForEach(sections) { section in
                    Section {
                        ForEach(section.messages) { m in row(m, last: m.id == lastID) }
                    } header: {
                        if let t = section.title {
                            Text(t.uppercased()).font(.inboxBody(12, medium: true)).tracking(0.6).foregroundStyle(p.secondary)
                                .padding(.horizontal, 16).padding(.vertical, 6)
                                .frame(maxWidth: .infinity, alignment: .leading).background(p.ground)
                        }
                    }
                    .listSectionSeparator(.hidden)
                }
            }
        }
        .listStyle(.plain).scrollContentBackground(.hidden).background(p.ground)
        .refreshable { await model.refresh() }
        .inboxNavigationChrome()
        .toolbar { toolbar }
        .safeAreaInset(edge: .bottom, spacing: 0) { bottom }
        .task { await model.observe() }
        .task(id: live.tick) { if live.refreshEnabled { await model.refresh() } }
    }

    private func row(_ m: MessageRecord, last: Bool) -> some View {
        MailRow(MailRowModel(m), mode: model.isSelecting ? .selecting(selected: model.selection.contains(m.id)) : .normal)
            .contentShape(Rectangle())
            .onTapGesture {
                if model.isSelecting { model.toggleSelection(m.id) } else { path.append(.message(m.id)) }
            }
            .onLongPressGesture { if !model.isSelecting { model.beginSelecting(m.id) } }
            // VoiceOver can't swipe: the same actions are offered in the rotor.
            .accessibilityAction(named: "Archive") { Task { await model.archive([m.id]) } }
            .accessibilityAction(named: m.isRead ? "Mark as unread" : "Mark as read") { Task { await model.toggleRead([m.id]) } }
            .accessibilityAction(named: "Select") { model.beginSelecting(m.id) }
            .accessibilityAddTraits(.isButton)
            .swipeActions(edge: .trailing, allowsFullSwipe: true) {
                if !model.isSelecting {
                    Button { Task { await model.archive([m.id]) } } label: { Label("Archive", systemImage: "archivebox") }
                        .tint(p.accent)
                    Button { Task { await model.toggleRead([m.id]) } } label: {
                        Label(m.isRead ? "Unread" : "Read", systemImage: m.isRead ? "envelope.badge" : "envelope.open")
                    }
                    .tint(p.secondary)
                }
            }
            .onAppear { if last { Task { await model.loadMore() } } }
            .plainRow()
    }

    @ToolbarContentBuilder private var toolbar: some ToolbarContent {
        if model.isSelecting {
            ToolbarItem(placement: .topBarLeading) { InboxToolbarButton("Select all") { model.selectAll() } }.inboxFlat()
            ToolbarItem(placement: .principal) { Text("\(model.selection.count) selected").font(.inboxBody(17, medium: true)).monospacedDigit().foregroundStyle(p.text) }
                .inboxFlat()
            ToolbarItem(placement: .topBarTrailing) { InboxToolbarButton("Done") { model.endSelecting() } }.inboxFlat()
        } else {
            InboxBackButton(backTitle) { if !path.isEmpty { path.removeLast() } }
            ToolbarItem(placement: .topBarTrailing) {
                Button { path.append(.search(scope: searchScope)) } label: { Image(systemName: "magnifyingglass").font(.system(size: 19, weight: .regular)) }
                    .buttonStyle(.plain).foregroundStyle(p.accent).frame(minWidth: 44, minHeight: 44)
                    .accessibilityLabel("Search")
            }.inboxFlat()
        }
    }

    @ViewBuilder private var bottom: some View {
        VStack(spacing: 8) {
            if let t = model.toast {
                Toast(t.message, undoTitle: t.kind == .archived ? "Undo" : nil) { Task { await model.undo() } }
                    .padding(.horizontal, 16).toastMotion(model.toast)
            }
            if model.isSelecting { bulkBar }
        }
        .padding(.bottom, 8)
    }

    private var bulkBar: some View {
        HStack(spacing: 0) {
            bulk("Mark read", "envelope.open") { Task { await model.toggleRead(model.selection) } }
            bulk("Archive", "archivebox") { Task { await model.archive(model.selection) } }
            bulk("Open in Gmail", "arrow.up.right.square") { model.openInGmail() }
        }
        .disabled(model.selection.isEmpty)
        .background(p.card)
        .overlay(alignment: .top) { Rectangle().fill(p.hairline).frame(height: 0.5) }
    }

    private func bulk(_ title: String, _ icon: String, _ action: @escaping () -> Void) -> some View {
        Button(action: action) {
            VStack(spacing: 4) {
                Image(systemName: icon)
                Text(title).font(.inboxBody(13))
            }
            .foregroundStyle(p.text).frame(maxWidth: .infinity, minHeight: 49)
        }
        .buttonStyle(.plain)
    }
}

private extension View {
    func plainRow() -> some View {
        listRowInsets(EdgeInsets()).listRowSeparator(.hidden).listRowBackground(Color.clear)
    }
}

/// Large title with a secondary line, as in the design's page headers.
struct PageHeader: View {
    let title: String
    let subtitle: String
    var body: some View { PageTitle(title, subtitle: subtitle) }
}
func unreadSubtitle(_ prefix: String?, _ unread: Int) -> String {
    let u = unread == 0 ? "all read" : "\(unread) unread"
    // At large text the line breaks after the "·" (it stays on the first line, glued to the domain), and the count never splits.
    return prefix.map { "\($0)\u{00A0}· \(u.replacingOccurrences(of: " ", with: "\u{00A0}"))" } ?? u
}
