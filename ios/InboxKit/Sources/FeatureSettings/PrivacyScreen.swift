import SwiftUI
import Observation
import API
import DesignSystem
import Domain

/// Privacy (ST04): where mail lives, retention for stored copies (`/v1/prefs`), and "Delete everything Inbox stored".
@MainActor @Observable
public final class PrivacyModel {
    public static let options: [(title: String, days: Int?)] = [("30 days", 30), ("90 days", 90), ("1 year", 365), ("Forever", nil)]
    public private(set) var retentionDays: Int? = 365
    public private(set) var loaded = false
    public private(set) var notice: String?
    // delete flow
    public private(set) var token: String?
    public var typed = ""
    public private(set) var working = false
    public private(set) var deleted: DataDeletionResult?
    public private(set) var deleteError: String?
    private let env: AppEnvironment
    public init(env: AppEnvironment) { self.env = env }

    public var retentionTitle: String { Self.options.first { $0.days == retentionDays }?.title ?? (retentionDays.map { "\($0) days" } ?? "Forever") }

    public func load() async {
        if let p = try? await env.api.prefs() { retentionDays = p.retentionDays; loaded = true; notice = nil }
        else if !loaded { notice = "Couldn't read your settings. Showing the defaults." }
    }
    public func setRetention(_ days: Int?) async {
        let before = retentionDays
        retentionDays = days
        do { try await env.api.updatePrefs(PrefsPatch(retention: days.map { .days($0) } ?? .keepForever)); notice = nil }
        catch { retentionDays = before; notice = "Couldn't change how long copies are kept." }
    }

    /// Step 1: the server hands out a short-lived token; the person types it to confirm.
    public func beginDelete() async {
        working = true; defer { working = false }
        deleted = nil; deleteError = nil; typed = ""
        do { token = try await env.api.confirmDataDeletion().token }
        catch { token = nil; deleteError = "Couldn't start. Check your connection and try again." }
    }
    public var canDelete: Bool { token != nil && typed.trimmingCharacters(in: .whitespaces).uppercased() == token && !working }

    /// Step 2: delete stored copies on the server, then empty the local cache. Gmail, businesses, rules stay.
    public func confirmDelete() async {
        guard canDelete, let token else { return }
        working = true; defer { working = false }
        do {
            deleted = try await env.api.deleteAllData(confirm: token)
            try? await env.database.clearMailCache()
            self.token = nil; typed = ""; deleteError = nil
        } catch let e as APIError {
            if case .problem(let p, _) = e, p.code == "confirmation_invalid" { deleteError = "That code expired or didn't match. Start again."; self.token = nil }
            else { deleteError = "Couldn't delete. Nothing was changed." }
        } catch { deleteError = "Couldn't delete. Nothing was changed." }
    }
    public func cancelDelete() { token = nil; typed = ""; deleteError = nil }
}

public struct PrivacyScreen: View {
    @Environment(\.palette) private var p
    @State private var model: PrivacyModel
    @State private var deleting = false
    public init(env: AppEnvironment) { _model = State(initialValue: PrivacyModel(env: env)) }

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                if let n = model.notice { Banner(.warning, title: n, systemImage: "exclamationmark.triangle") }
                if let d = model.deleted {
                    Banner(.ok, title: "Deleted.", message: "\(d.deletedMessages.formatted()) stored messages removed. Gmail is untouched.", systemImage: "checkmark.circle", inline: true)
                }
                InsetGroup("Where your mail lives") {
                    InsetRow("Your Inbox server", subtitle: "Stored copies and the search index")
                    InsetRow("Your Cloudflare account", subtitle: "Receives your mail and passes it on")
                    InsetRow("Your Gmail", subtitle: "Unchanged, except labels and archive")
                    InsetRow("Alfather", subtitle: "Reads from your Inbox server; never sent to a cloud AI", showsDivider: false)
                }
                InsetGroup("Keep") {
                    Menu {
                        ForEach(Array(PrivacyModel.options.enumerated()), id: \.offset) { _, o in
                            Button(o.title) { Task { await model.setRetention(o.days) } }
                        }
                    } label: {
                        InsetRow("Stored copies", showsDivider: false) {
                            HStack(spacing: 6) {
                                Text(model.retentionTitle).font(.inboxBody(16)).foregroundStyle(p.secondary)
                                Image(systemName: "chevron.up.chevron.down").font(.system(size: 12)).foregroundStyle(p.hairlineStrong)
                            }
                        }
                    }.buttonStyle(.plain).accessibilityLabel("Keep stored copies for \(model.retentionTitle)")
                }
                InsetGroup {
                    Button { deleting = true; Task { await model.beginDelete() } } label: {
                        Text("Delete everything Inbox stored").font(.inboxBody(16.5)).foregroundStyle(p.destructive).frame(maxWidth: .infinity, minHeight: 56).contentShape(Rectangle())
                    }.buttonStyle(.plain)
                }
                Text("Deleting stored copies never touches Gmail.").font(.inboxBody(13.5)).foregroundStyle(p.secondary).padding(.horizontal, 4)
            }.padding(16)
        }
        .background(p.ground)
        .inboxPushed(title: "Privacy", back: "Settings")
        .sheet(isPresented: $deleting, onDismiss: { model.cancelDelete() }) { deleteSheet.environment(\.palette, p).presentationDetents([.medium, .large]) }
        .task {
            await model.load()
            // Screenshot aid (mock mode): `-OpenDeleteSheet 1` opens the delete-everything confirmation.
            if UserDefaults.standard.bool(forKey: "OpenDeleteSheet") { deleting = true; await model.beginDelete() }
        }
    }

    private var deleteSheet: some View {
        VStack(alignment: .leading, spacing: 16) {
            SheetHeader("Delete everything?", trailingTitle: "Cancel") { deleting = false }
            VStack(alignment: .leading, spacing: 14) {
                Text("This removes every stored copy of your mail from your Inbox server and this phone. Gmail, your businesses and your rules stay as they are.")
                    .font(.inboxBody(15)).foregroundStyle(p.secondary)
                if let e = model.deleteError { Banner(.error, title: e, systemImage: "exclamationmark.triangle") }
                if let t = model.token {
                    Text("Type \(t) to confirm").font(.inboxBody(15, medium: true)).foregroundStyle(p.text)
                    TextField("", text: $model.typed, prompt: Text("Type the code").foregroundColor(p.textTertiary))
                        .textInputAutocapitalization(.characters).autocorrectionDisabled().font(.inboxMono(16)).foregroundStyle(p.text)
                        .padding(.horizontal, 12).frame(height: 48)
                        .background(RoundedRectangle(cornerRadius: 10, style: .continuous).fill(p.field))
                    PrimaryButton(model.working ? "Deleting…" : "Delete everything", style: .destructive) {
                        Task { await model.confirmDelete(); if model.deleted != nil { deleting = false } }
                    }.disabled(!model.canDelete).opacity(model.canDelete ? 1 : 0.4)
                } else if model.working { ProgressView().frame(maxWidth: .infinity) }
                else { PrimaryButton("Try again", style: .secondary) { Task { await model.beginDelete() } } }
            }.padding(.horizontal, 16)
            Spacer()
        }
        .background(p.ground)
    }
}
