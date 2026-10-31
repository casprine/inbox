import SwiftUI
import DesignSystem
import Domain
import FeatureBusinesses
import FeatureGmail
import Persistence

/// Mail root: Needs attention (only when non-empty), All businesses, then each business.
public struct BusinessesView: View {
    @Environment(\.palette) private var p
    @State private var model: BusinessesModel
    @State private var editing: Bool
    @Environment(ChromeState.self) private var chrome: ChromeState?
    @State private var editModel: BusinessesSettingsModel
    let live: LiveEvents
    let env: AppEnvironment
    let openImport: () -> Void
    @Binding var path: [Route]

    public init(env: AppEnvironment, live: LiveEvents, path: Binding<[Route]>, openImport: @escaping () -> Void = {}, startEditing: Bool = UserDefaults.standard.bool(forKey: "OpenBusinessesEdit")) {
        self.env = env; self.openImport = openImport
        _editing = State(initialValue: startEditing)
        _editModel = State(initialValue: BusinessesSettingsModel(env: env))
        _model = State(initialValue: BusinessesModel(env: env))
        self.live = live; _path = path
    }

    public var body: some View {
        Group {
            if editing { editor } else { browse }
        }
        .background(p.ground)
        .inboxNavigationChrome()
        .task { await model.observe() }
        .task { await editModel.observe() }
        .onChange(of: editing, initial: true) { chrome?.hidesTabBar = editing }
        .onDisappear { chrome?.hidesTabBar = false }
        .task(id: live.tick) { if live.refreshEnabled { await model.refresh() } }
    }

    /// BZ02 on the Mail root: a centred "Businesses" title in the nav, Done on the right, no tab bar.
    private var editor: some View {
        BusinessEditList(model: editModel)
            .toolbar {
                ToolbarItem(placement: .principal) { Text("Businesses").font(.inboxBody(17, medium: true)).foregroundStyle(p.text) }
                ToolbarItem(placement: .topBarTrailing) { InboxToolbarButton("Done") { withAnimation { editing = false } } }
            }
    }

    private var titleRow: some View {
        HStack {
            PageTitle("Businesses")
            InboxToolbarButton(editing ? "Done" : "Edit") { withAnimation { editing.toggle() } }
                .disabled(model.businesses.isEmpty).opacity(model.businesses.isEmpty ? 0.5 : 1).padding(.trailing, 20)
        }
    }

    private var browse: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                titleRow
                Button { path.append(.search(scope: nil)) } label: { searchField }
                    .buttonStyle(.plain).padding(.horizontal, 16).padding(.bottom, 14)
                VStack(alignment: .leading, spacing: 18) {
                    if model.refreshFailed && !model.businesses.isEmpty {
                        Banner(.warning, title: "Couldn't refresh", message: "Showing what's saved on this phone.")
                    }
                    GmailSignedOutBanner(env: env)
                    ImportProgressBanner(env: env, open: openImport)
                    if model.showsAttention { attentionGroup }
                    if model.isLoading { skeleton }
                    else if model.visible.isEmpty && model.refreshFailed { offline }
                    else if model.visible.isEmpty { empty } else { lists }
                }
                .padding(.horizontal, 16).padding(.bottom, 24)
            }
        }
        .refreshable { await model.refresh() }
    }

    private var searchField: some View {
        HStack(spacing: 8) {
            Image(systemName: "magnifyingglass").font(.inboxBody(16)).foregroundStyle(p.secondary)
            Text("Search").font(.inboxBody(17)).foregroundStyle(p.textTertiary)
            Spacer()
        }
        .padding(.horizontal, 10).frame(minHeight: 38)
        .background(RoundedRectangle(cornerRadius: 10, style: .continuous).fill(p.field))
        .accessibilityElement(children: .ignore).accessibilityLabel("Search").accessibilityAddTraits(.isButton)
    }

    private var attentionGroup: some View {
        InsetGroup("Needs attention") {
            if model.unroutedCount > 0 {
                row("Unrouted", subtitle: "No rule matched", count: model.unroutedCount, divider: model.failedCount > 0, route: .unrouted, accentCount: true)
            }
            if model.failedCount > 0 {
                row("Failed", subtitle: "Couldn't be saved", count: model.failedCount, divider: false, route: .failed, accentCount: true)
            }
        }
    }

    private var lists: some View {
        VStack(alignment: .leading, spacing: 18) {
            InsetGroup {
                row("All businesses", subtitle: nil, count: model.totalUnread, divider: false, route: .allBusinesses, icon: true)
            }
            InsetGroup("\(model.visible.count) business\(model.visible.count == 1 ? "" : "es")") {
                ForEach(Array(model.visible.enumerated()), id: \.element.id) { i, b in
                    row(b.name, subtitle: b.domain, count: b.unread, divider: i < model.visible.count - 1,
                        route: .business(b.id, address: nil))
                }
            }
        }
    }

    private func row(_ title: String, subtitle: String?, count: Int, divider: Bool, route: Route, accentCount: Bool = false, icon: Bool = false) -> some View {
        Button { path.append(route) } label: {
            InsetRow(title, subtitle: subtitle, showsDivider: divider, systemImage: icon ? "tray" : nil) {
                HStack(spacing: 8) {
                    if count > 0 {
                        Text("\(count)").font(.inboxBody(16)).foregroundStyle(accentCount ? p.accentText : p.secondary)
                            .accessibilityLabel("\(count) \(accentCount ? "need attention" : "unread")")
                    }
                    Image(systemName: "chevron.right").font(.system(size: 14, weight: .semibold)).foregroundStyle(p.hairlineStrong)
                }
            }
        }
        .buttonStyle(.plain)
    }

    private var skeleton: some View {
        InsetGroup("Businesses") {
            ForEach(0..<4, id: \.self) { _ in
                InsetRow("Business name", subtitle: "domain.example").redacted(reason: .placeholder)
            }
        }
    }

    private var offline: some View {
        EmptyState(title: "Can't reach your server", message: "Check your connection. Pull down to try again.", actionTitle: "Try again") { Task { await model.refresh() } }
    }

    private var empty: some View {
        EmptyState(title: "No businesses yet", message: "Add a business in Settings and its mail will show up here.")
    }
}
