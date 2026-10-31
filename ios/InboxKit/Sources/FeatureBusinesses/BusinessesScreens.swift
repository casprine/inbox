import SwiftUI
import API
import DesignSystem
import Domain
import Persistence

/// BZ01 list / BZ02 edit (reorder + hide). Navigation is owned by the Settings stack through the closures.
public struct BusinessesSettingsScreen: View {
    @Environment(\.palette) private var p
    @State private var model: BusinessesSettingsModel
    @State private var editMode: EditMode = .inactive
    let onOpen: (UUID) -> Void
    let onAdd: () -> Void

    public init(env: AppEnvironment, onOpen: @escaping (UUID) -> Void, onAdd: @escaping () -> Void) {
        _model = State(initialValue: BusinessesSettingsModel(env: env)); self.onOpen = onOpen; self.onAdd = onAdd
    }

    public var body: some View {
        Group {
            if editMode == .active {
                BusinessEditList(model: model)
            } else {
                ScrollView {
                    VStack(alignment: .leading, spacing: 18) {
                        if let n = model.notice { Banner(.warning, title: n, systemImage: "exclamationmark.triangle") }
                        InsetGroup("\(model.businesses.count) business\(model.businesses.count == 1 ? "" : "es")") {
                            ForEach(Array(model.businesses.enumerated()), id: \.element.id) { i, b in
                                Button { onOpen(b.id) } label: {
                                    InsetRow(b.name, subtitle: b.hidden ? "\(b.domain) · Hidden" : b.domain, showsDivider: true) {
                                        HStack(spacing: 8) {
                                            if b.routing != "ok" { Circle().fill(Color.hex(0xC27803)).frame(width: 8, height: 8).accessibilityLabel("Routing problem") }
                                            Image(systemName: "chevron.right").font(.system(size: 14, weight: .semibold)).foregroundStyle(p.hairlineStrong)
                                        }
                                    }
                                }.buttonStyle(.plain)
                            }
                            Button(action: onAdd) {
                                Text("Add a business").font(.inboxBody(16.5)).foregroundStyle(p.accentText)
                                    .frame(maxWidth: .infinity, minHeight: 56, alignment: .leading).padding(.horizontal, 16).contentShape(Rectangle())
                            }.buttonStyle(.plain)
                        }
                        if !model.businesses.isEmpty {
                            Text("Order here is the order on the Mail tab. Tap Edit to reorder or hide.").font(.inboxBody(13.5)).foregroundStyle(p.secondary).padding(.horizontal, 4)
                        }
                        if model.businesses.isEmpty && model.hasLoaded {
                            EmptyState(title: "No businesses yet", message: "Add a domain from Cloudflare and its mail shows up in Mail.")
                        }
                    }.padding(16)
                }
                .refreshable { await model.refresh() }
            }
        }
        .environment(\.editMode, $editMode)
        .background(p.ground)
        .inboxPushed(title: "Businesses", back: "Settings")
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                HStack(spacing: 12) {
                    if !model.businesses.isEmpty {
                        InboxToolbarButton(editMode == .active ? "Done" : "Edit") { withAnimation { editMode = editMode == .active ? .inactive : .active } }
                    }
                    Button(action: onAdd) { Image(systemName: "plus").font(.system(size: 20)) }
                        .buttonStyle(.plain).foregroundStyle(p.accent).frame(minWidth: 44, minHeight: 44).accessibilityLabel("Add a business")
                }
            }.inboxFlat()
        }
        .task { await model.observe() }
        .task { await model.refresh() }
    }
}

/// BZ02: drag to reorder and a switch per business to hide it from Mail. Shared by Settings → Businesses and the Mail root.
public struct BusinessEditList: View {
    @Environment(\.palette) private var p
    let model: BusinessesSettingsModel
    public init(model: BusinessesSettingsModel) { self.model = model }

    /// One card: rounded only at the top of the first row and the bottom of the last, 16 pt from the screen edges (BZ02, radius 12).
    private func card(_ i: Int, _ n: Int) -> some View {
        UnevenRoundedRectangle(topLeadingRadius: i == 0 ? 12 : 0, bottomLeadingRadius: i == n - 1 ? 12 : 0,
                               bottomTrailingRadius: i == n - 1 ? 12 : 0, topTrailingRadius: i == 0 ? 12 : 0, style: .continuous)
            .fill(p.card).padding(.horizontal, 16)
    }

    public var body: some View {
        List {
            Text("Order here is the order on the Mail tab. Drag to reorder; switch off to hide a business from Mail.")
                .font(.inboxBody(13.5)).foregroundStyle(p.secondary).padding(.horizontal, 20).padding(.vertical, 8)
                .listRowInsets(EdgeInsets()).listRowBackground(Color.clear).listRowSeparator(.hidden)
            if let n = model.notice {
                Banner(.warning, title: n, systemImage: "exclamationmark.triangle").padding(.horizontal, 16)
                    .listRowInsets(EdgeInsets()).listRowBackground(Color.clear).listRowSeparator(.hidden)
            }
            let all = model.businesses
            ForEach(Array(all.enumerated()), id: \.element.id) { i, b in
                HStack(spacing: 12) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(b.name).font(.inboxBody(16.5)).foregroundStyle(p.text)
                        Text(b.domain).font(.inboxBody(14)).foregroundStyle(p.secondary)
                    }
                    Spacer(minLength: 8)
                    SquareSwitch(isOn: Binding(get: { !b.hidden }, set: { on in Task { await model.setHidden(!on, b) } }))
                        .accessibilityLabel("Show \(b.name) in Mail")
                }
                .padding(.horizontal, 32).frame(minHeight: 56)
                .overlay(alignment: .bottom) { if i < all.count - 1 { Rectangle().fill(p.hairline).frame(height: 1).padding(.leading, 32).padding(.trailing, 16) } }
                .accessibilityElement(children: .combine)
                .accessibilityActions {
                    if model.canMove(b, by: -1) { Button("Move up") { Task { await model.move(b, by: -1) } } }
                    if model.canMove(b, by: 1) { Button("Move down") { Task { await model.move(b, by: 1) } } }
                }
                .listRowInsets(EdgeInsets()).listRowSeparator(.hidden).listRowBackground(card(i, all.count))
            }
            .onMove { f, t in Task { await model.move(from: f, to: t) } }
        }
        .listStyle(.plain).scrollContentBackground(.hidden).background(p.ground)
        .environment(\.defaultMinListRowHeight, 56)
        .environment(\.editMode, .constant(.active))
    }
}

/// BZ03 detail with BZ04 remove alert.
public struct BusinessDetailScreen: View {
    @Environment(\.palette) private var p
    @State private var model: BusinessDetailModel
    @FocusState private var nameFocused: Bool
    let onRouting: () -> Void
    let onRemoved: () -> Void

    public init(env: AppEnvironment, id: UUID, onRouting: @escaping () -> Void, onRemoved: @escaping () -> Void) {
        _model = State(initialValue: BusinessDetailModel(env: env, id: id)); self.onRouting = onRouting; self.onRemoved = onRemoved
    }

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                if let n = model.notice { Banner(.warning, title: n, systemImage: "exclamationmark.triangle") }
                if let b = model.business {
                    InsetGroup("Business") {
                        RuleLikeRow("Name") {
                            TextField("Name", text: $model.name).focused($nameFocused).submitLabel(.done)
                                .onSubmit { Task { await model.commitName() } }
                        }
                        RuleLikeRow("Domain") { Text(b.domain) }
                        Button(action: onRouting) {
                            let r = BusinessesSettingsModel.routingText(b.routing)
                            RuleLikeRow("Routing") {
                                HStack(spacing: 8) {
                                    StatusDot(r.ok ? .ok : .warning)
                                    Text("Catch-all · \(r.value)").foregroundStyle(p.text)
                                    Spacer()
                                    if !r.ok { Image(systemName: "chevron.right").font(.system(size: 14, weight: .semibold)).foregroundStyle(p.hairlineStrong) }
                                }
                            }
                        }.buttonStyle(.plain)
                        RuleLikeRow("Last message", divider: false) { Text(b.lastMessageAt.map { Self.lastMessage($0) } ?? "No mail yet") }
                    }
                    InsetGroup {
                        InsetRow("Import older mail", showsDivider: false) { Text(model.importSummary).font(.inboxBody(16)).foregroundStyle(p.secondary) }
                    }
                    if !model.addresses.isEmpty {
                        InsetGroup("Addresses seen") {
                            ForEach(Array(model.addresses.enumerated()), id: \.offset) { i, a in
                                InsetRow("\(a.localPart)@", showsDivider: i < model.addresses.count - 1) {
                                    Text(a.count.formatted()).font(.inboxBody(16)).foregroundStyle(p.secondary)
                                }
                            }
                        }
                    }
                    InsetGroup {
                        InsetRow("Hide from Mail", showsDivider: false) {
                            SquareSwitch(isOn: Binding(get: { b.hidden }, set: { h in Task { await model.setHidden(h) } })).accessibilityLabel("Hide from Mail")
                        }
                    }
                    InsetGroup {
                        Button { model.confirmingRemove = true } label: {
                            Text("Remove business").font(.inboxBody(16.5)).foregroundStyle(p.destructive).frame(maxWidth: .infinity, minHeight: 56).contentShape(Rectangle())
                        }.buttonStyle(.plain)
                    }
                } else { ProgressView().frame(maxWidth: .infinity).padding(.top, 40) }
            }
            .padding(16).padding(.bottom, 24)
        }
        .background(p.ground)
        .inboxPushed(title: model.business?.name ?? "", back: "Businesses")
        .alert("Remove \(model.business?.name ?? "this business")?", isPresented: $model.confirmingRemove) {
            Button("Remove", role: .destructive) { Task { if await model.remove() { onRemoved() } } }
            Button("Cancel", role: .cancel) {}
        } message: { Text("Its mail stays in Gmail. Inbox stops showing it and deletes its stored copies. Rules for it are turned off.") }
        .onChange(of: nameFocused) { if !nameFocused { Task { await model.commitName() } } }
        .task { await model.load() }
        .task { await model.observe() }
    }
}

extension BusinessDetailScreen {
    /// Today: the time ("7:26 PM"); within a week: weekday; older: "Oct 5" (no year within the year).
    static func lastMessage(_ d: Date, now: Date = .now, calendar: Calendar = .current) -> String {
        if calendar.isDate(d, inSameDayAs: now) { return d.formatted(date: .omitted, time: .shortened) }
        if let n = calendar.dateComponents([.day], from: calendar.startOfDay(for: d), to: calendar.startOfDay(for: now)).day, n >= 0, n < 7 {
            return d.formatted(.dateTime.weekday(.wide))
        }
        return calendar.isDate(d, equalTo: now, toGranularity: .year) ? d.formatted(.dateTime.month(.abbreviated).day()) : d.formatted(.dateTime.month(.abbreviated).day().year())
    }
}

/// Caption on the left, content on the right: the detail screens' form row.
struct RuleLikeRow<Content: View>: View {
    @Environment(\.palette) private var p
    let title: String
    var divider = true
    @ViewBuilder var content: Content
    init(_ title: String, divider: Bool = true, @ViewBuilder content: () -> Content) { self.title = title; self.divider = divider; self.content = content() }
    var body: some View {
        HStack(spacing: 12) {
            Text(title).font(.inboxBody(15)).foregroundStyle(p.secondary).lineLimit(1).frame(width: 104, alignment: .leading)
            content.font(.inboxBody(16.5)).foregroundStyle(p.text).frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.horizontal, 16).frame(minHeight: 52).contentShape(Rectangle())
        .overlay(alignment: .bottom) { if divider { Rectangle().fill(p.hairline).frame(height: 1).padding(.leading, 16) } }
    }
}

/// BZ05.
public struct AddBusinessSheet: View {
    @Environment(\.palette) private var p
    @Environment(\.dismiss) private var dismiss
    @State private var model: AddBusinessModel
    public init(env: AppEnvironment) { _model = State(initialValue: AddBusinessModel(env: env)) }

    public var body: some View {
        VStack(spacing: 0) {
            SheetHeader("Add a business", trailingTitle: "Cancel") { dismiss() }
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    Text("Pick a domain from Cloudflare.").font(.inboxBody(15)).foregroundStyle(p.secondary)
                    if let e = model.error { Banner(.warning, title: e, systemImage: "exclamationmark.triangle") }
                    if model.isLoading { ProgressView().frame(maxWidth: .infinity) }
                    InsetGroup {
                        ForEach(Array(model.domains.enumerated()), id: \.element.id) { i, d in
                            Button { model.select(d) } label: {
                                HStack {
                                    VStack(alignment: .leading, spacing: 2) {
                                        Text(d.domain).font(.inboxBody(16.5)).foregroundStyle(d.selectable ? p.text : p.secondary)
                                        Text(sub(d)).font(.inboxBody(14)).foregroundStyle(p.secondary)
                                    }
                                    Spacer()
                                    if d.selectable {
                                        ZStack {
                                            Circle().strokeBorder(model.selected == d.id ? p.accent : p.hairlineStrong, lineWidth: 1.5)
                                            if model.selected == d.id { Circle().fill(p.accent).frame(width: 12, height: 12) }
                                        }.frame(width: 22, height: 22)
                                    }
                                }
                                .padding(.horizontal, 16).frame(minHeight: 60).contentShape(Rectangle())
                                .overlay(alignment: .bottom) { if i < model.domains.count - 1 { Rectangle().fill(p.hairline).frame(height: 1).padding(.leading, 16) } }
                            }.buttonStyle(.plain).disabled(!d.selectable)
                        }
                    }
                    Text("Only domains on your Cloudflare account are listed.").font(.inboxBody(14)).foregroundStyle(p.secondary)
                }.padding(16)
            }
            PrimaryButton(model.chosen.map { "Add \($0.domain)" } ?? "Add") { Task { if await model.add() { dismiss() } } }
                .disabled(model.chosen == nil || model.isSaving).opacity(model.chosen == nil ? 0.4 : 1).padding(16)
        }
        .background(p.ground)
        .task { await model.load() }
    }

    private func sub(_ d: CloudflareDomain) -> String {
        switch d.routing {
        case .ok: d.selectable ? "Ready" : "Not active on Cloudflare"
        case .toEmail: "Catch-all sends to email"
        case .none: "No Email Routing"
        case .unknown: "Couldn't check routing"
        }
    }
}

/// BZ06.
public struct RoutingProblemScreen: View {
    @Environment(\.palette) private var p
    @Environment(\.openURL) private var openURL
    @State private var model: RoutingProblemModel
    public init(env: AppEnvironment, id: UUID) { _model = State(initialValue: RoutingProblemModel(env: env, id: id)) }

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                if let b = model.business {
                    if model.isFixed {
                        Banner(.ok, title: "Routing is fixed.", message: "Mail for \(b.domain) is reaching Inbox again.", systemImage: "checkmark.circle", inline: true)
                    } else {
                        Banner(.error, title: model.quietDays.map { "No mail for \($0) days." } ?? "Mail may not be arriving.",
                               message: "The catch-all for \(b.domain) is sending straight to email again, so Inbox isn't seeing new mail.",
                               systemImage: "exclamationmark.triangle", inline: true)
                    }
                    if let e = model.error { Banner(.error, title: e, systemImage: "exclamationmark.triangle") }
                    InsetGroup("Routing") {
                        let r = BusinessesSettingsModel.routingText(model.result?.rawValue ?? b.routing)
                        RuleLikeRow("Catch-all") { Text(r.value) }
                        RuleLikeRow("Last message", divider: false) { Text(b.lastMessageAt.map { $0.formatted(date: .abbreviated, time: .omitted) } ?? "None") }
                    }
                    InsetGroup("How to fix") {
                        InsetRow("1 · Open Email Routing", subtitle: "Cloudflare dashboard → \(b.domain)")
                        InsetRow("2 · Edit the catch-all rule", subtitle: "Action: Send to a Worker")
                        InsetRow("3 · Choose the worker", subtitle: "inbox-worker")
                        InsetRow("4 · Save", showsDivider: false)
                    }
                    PrimaryButton("Open Cloudflare", style: .secondary) { openURL(URL(string: "https://dash.cloudflare.com")!) }
                    PrimaryButton(model.checking ? "Checking…" : "Check again") { Task { await model.checkAgain() } }.disabled(model.checking)
                } else { ProgressView().frame(maxWidth: .infinity).padding(.top, 40) }
            }.padding(16)
        }
        .background(p.ground)
        .inboxPushed(title: model.business?.name ?? "Routing", back: "Businesses")
        .task { await model.load() }
    }
}
