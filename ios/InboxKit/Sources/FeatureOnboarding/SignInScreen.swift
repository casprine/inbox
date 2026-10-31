import SwiftUI
import UIKit
import API
import DesignSystem

/// OB01b: server address + password (D4 §1). Pushed over the welcome step; the two welcome buttons only choose the mode.
struct SignInStep: View {
    @Environment(\.palette) private var p
    @Environment(\.dynamicTypeSize) private var typeSize
    @Environment(\.scenePhase) private var scenePhase
    let onboarding: OnboardingModel
    @Bindable var model: SignInModel
    /// Off in snapshot tests that render states with their own focus.
    var autoFocus = true
    /// Draws the focus ring on a row without focusing it (snapshots: a real focus raises the keyboard and scrolls the screen).
    var ringOverride: SignInField?
    @FocusState private var focus: SignInField?
    @AccessibilityFocusState private var titleFocused: Bool
    @AccessibilityFocusState private var errorFocused: Bool
    @State private var task: Task<Void, Never>?

    private var accessible: Bool { typeSize.isAccessibilitySize }

    var body: some View {
        VStack(spacing: 0) {
            navRow
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    header
                    fieldGroup.padding(.top, 24).padding(.horizontal, 16)
                    lockBanner
                    if case .unreachable(let kind)? = model.issue { unreachableBanner(kind) }
                    helpers.padding(.top, 8).padding(.horizontal, 20)
                    if accessible { signInButton.padding(.horizontal, 16).padding(.top, 24).padding(.bottom, 24) }
                }
                .padding(.bottom, 16)
            }
            .scrollDismissesKeyboard(.interactively)
            if !accessible { signInButton.padding(.horizontal, 16).padding(.bottom, 16).background(p.ground) }
        }
        .background(p.ground)
        .task {
            titleFocused = true
            guard autoFocus else { return }
            try? await Task.sleep(for: .milliseconds(350))
            focus = model.initialFocus == .password ? (model.revealed ? .passwordShown : .password) : .address
        }
        .onChange(of: model.focusRequest) { _, r in if let r { focus = r.field == .password && model.revealed ? .passwordShown : r.field } }
        .onChange(of: model.issue) { _, i in if i != nil { errorFocused = true } }
        .onChange(of: scenePhase) { _, s in if s != .active { model.revealed = false } }
        .onDisappear { model.revealed = false }
    }

    // MARK: Pieces

    private var navRow: some View {
        HStack {
            Button { task?.cancel(); model.cancelled(); onboarding.back() } label: {
                HStack(spacing: 4) { Image(systemName: "chevron.left").font(.system(size: 19, weight: .semibold)); Text("Back").font(.inboxBody(17)) }
                    .foregroundStyle(p.accentText).frame(minHeight: 44)
            }.buttonStyle(.plain).accessibilityLabel("Back")
            Spacer()
        }
        .padding(.horizontal, 16)
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text(SignInCopy.title(model.mode)).font(.inboxHeadingBold(27, relativeTo: .title2)).tracking(-0.5).foregroundStyle(p.text)
                .padding(.top, 8).accessibilityAddTraits(.isHeader).accessibilityFocused($titleFocused)
            Text(SignInCopy.caption(model.mode)).font(.inboxBody(15, relativeTo: .subheadline)).lineSpacing(3).foregroundStyle(p.secondary)
                .padding(.top, 12)
        }
        .frame(maxWidth: .infinity, alignment: .leading).padding(.horizontal, 20)
    }

    private var fieldGroup: some View {
        VStack(spacing: 0) {
            SignInRow(label: "Server address", focused: focus == .address || (focus == nil && ringOverride == .address), errored: model.issue?.field == .address) {
                TextField("Server address", text: $model.address, prompt: Text("inbox.example.com").foregroundColor(p.textTertiary))
                    .focused($focus, equals: .address)
                    .keyboardType(.URL).textContentType(.URL).textInputAutocapitalization(.never).autocorrectionDisabled()
                    .submitLabel(.next).onSubmit { focus = .password }
                    .font(.inboxBody(17)).foregroundStyle(p.text).tint(p.accent)
                    .disabled(model.isLoading)
                    .accessibilityLabel("Server address")
                    .accessibilityHint("The address you gave your Inbox server, for example inbox.example.com.")
                    .accessibilityValue(model.issue?.field == .address ? "Error: \(model.issue?.inlineText ?? "")" : (model.address.isEmpty ? "empty" : model.address))
            }
            .overlay(alignment: .bottom) { Rectangle().fill(p.hairline).frame(height: 1).padding(.leading, 16) }
            SignInRow(label: "Password", focused: passwordFocused || (focus == nil && ringOverride == .password), errored: model.issue?.field == .password) {
                HStack(spacing: 0) {
                    passwordField
                    Button { toggleReveal() } label: {
                        Image(systemName: model.revealed ? "eye.slash" : "eye").font(.system(size: 20)).foregroundStyle(p.secondary)
                            .frame(width: 44, height: 44).contentShape(Rectangle())
                    }
                    .buttonStyle(.plain).accessibilityLabel(model.revealed ? "Hide password" : "Show password")
                    .padding(.trailing, -12).padding(.vertical, -10)
                }
            }
        }
        .background(RoundedRectangle(cornerRadius: 12, style: .continuous).fill(p.card))
        .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
        .opacity(model.isLoading ? 0.6 : 1)
    }

    private var passwordPrompt: Text { Text(typeSize >= .accessibility3 ? "Password" : "Your server password").foregroundColor(p.textTertiary) }

    /// Both fields stay in the view tree and share one binding; removing a secure field with text in it makes iOS offer "Save Password?"
    /// every time the eye is tapped. The hidden one is invisible, not tappable and hidden from VoiceOver.
    @ViewBuilder private var passwordField: some View {
        ZStack(alignment: .leading) {
            SecureField("Password", text: $model.password, prompt: passwordPrompt)
                .focused($focus, equals: .password)
                .textContentType(.password)
                .opacity(model.revealed ? 0 : 1).allowsHitTesting(!model.revealed).accessibilityHidden(model.revealed)
            TextField("Password", text: $model.password, prompt: passwordPrompt)
                .focused($focus, equals: .passwordShown)
                .opacity(model.revealed ? 1 : 0).allowsHitTesting(model.revealed).accessibilityHidden(!model.revealed)
        }
        .textInputAutocapitalization(.never).autocorrectionDisabled()
        .submitLabel(.go).onSubmit { if model.canSubmit { submit() } }
        .font(.inboxBody(17)).foregroundStyle(p.text).tint(p.accent)
        .disabled(model.isLoading)
        .accessibilityLabel("Password")
        .accessibilityHint("Set on the server, not in this app.")
        .accessibilityValue(model.issue?.field == .password ? "Error: \(model.issue?.inlineText ?? "")" : (model.revealed ? model.password : ""))
    }

    private var passwordFocused: Bool { focus == .password || focus == .passwordShown }

    private func toggleReveal() {
        let hadFocus = passwordFocused
        model.revealed.toggle()
        if hadFocus { focus = model.revealed ? .passwordShown : .password }
    }

    /// One block under the group: the address line, then the password line; an error replaces the line of its own row.
    private var helpers: some View {
        VStack(alignment: .leading, spacing: 4) {
            line(for: .address, helper: "The address you gave your Inbox server.")
            line(for: .password, helper: "Set on the server, not in this app. Forgot it? Change OWNER_PASSWORD there and restart the server.")
        }
    }

    @ViewBuilder private func line(for field: SignInField, helper: String) -> some View {
        if model.issue?.field == field, let text = model.issue?.inlineText {
            HStack(alignment: .firstTextBaseline, spacing: 6) {
                Image(systemName: "exclamationmark.circle").font(.system(size: 14)).accessibilityHidden(true)
                Text(text).font(.inboxBody(13.5, medium: true, relativeTo: .footnote)).fixedSize(horizontal: false, vertical: true)
            }
            .foregroundStyle(p.accentText)
            .accessibilityElement(children: .ignore).accessibilityLabel("Error: \(text)")
            .accessibilityFocused($errorFocused)
        } else {
            Text(helper).font(.inboxBody(13.5, relativeTo: .footnote)).foregroundStyle(p.secondary).fixedSize(horizontal: false, vertical: true)
        }
    }

    @ViewBuilder private var lockBanner: some View {
        TimelineView(.periodic(from: .now, by: model.lockUntil == nil ? 3_600 : 1)) { _ in
            if let remaining = model.lockRemaining() {
                SignInBanner(icon: "clock.badge.exclamationmark", lead: "Too many tries.", message: SignInCopy.lockMessage(remaining: remaining),
                             spoken: "Too many tries. Sign-in is paused to protect your server. Try again in \(SignInCopy.spoken(remaining)).")
                    .accessibilityFocused($errorFocused)
                    .padding(.top, 12).padding(.horizontal, 16)
            }
        }
        .task(id: model.lockUntil) { await watchLock() }
    }

    private func unreachableBanner(_ kind: SignInModel.Unreachable) -> some View {
        let t = SignInModel.bannerText(kind)
        return SignInBanner(icon: "exclamationmark.triangle", lead: t.title, message: t.message, spoken: t.title + " " + t.message,
                            action: ("Try again", { submit() }))
            .accessibilityFocused($errorFocused)
            .padding(.top, 12).padding(.horizontal, 16)
    }

    /// Announces the start and the end of a lock-out (not every second) and refreshes the screen when it ends.
    private func watchLock() async {
        guard let until = model.lockUntil, let first = model.lockRemaining() else { return }
        UIAccessibility.post(notification: .announcement, argument: "Sign-in is paused. Try again in \(SignInCopy.spoken(first)).")
        let wait = until.timeIntervalSince(.now)
        guard wait > 0 else { return }
        try? await Task.sleep(for: .seconds(wait))
        if !Task.isCancelled { UIAccessibility.post(notification: .announcement, argument: "You can try signing in again.") }
    }

    /// Re-evaluated every second only while a lock-out is on screen; otherwise a plain view (a ticking view would rebuild the
    /// button under VoiceOver and UI-test taps).
    @ViewBuilder private var signInButton: some View {
        if model.lockUntil != nil {
            TimelineView(.periodic(from: .now, by: 1)) { _ in buttonBody }
        } else { buttonBody }
    }

    private var buttonBody: some View {
        Group {
            let remaining = model.lockRemaining()
            let label = remaining.map(SignInCopy.lockButton) ?? "Sign in"
            let enabled = model.canSubmit
            Button { submit() } label: {
                ZStack {
                    Text(label).font(.inboxBody(17, medium: true)).foregroundStyle(p.onAccent).multilineTextAlignment(.center)
                        .opacity(model.isLoading ? 0 : 1)
                    if model.isLoading { ProgressView().tint(p.onAccent).accessibilityHidden(true) }
                }
                .frame(maxWidth: .infinity, minHeight: 52).padding(.vertical, accessible ? 8 : 0)
                .background(RoundedRectangle(cornerRadius: 14, style: .continuous).fill(p.accentFill))
            }
            .buttonStyle(.plain)
            .disabled(!enabled)
            .opacity(enabled || model.isLoading ? 1 : 0.4)
            .accessibilityLabel(model.isLoading ? "Signing in" : remaining.map { "Try again in \(SignInCopy.spoken($0))" } ?? "Sign in")
            .accessibilityAddTraits(model.isLoading ? .updatesFrequently : [])
        }
    }

    private func submit() {
        guard model.canSubmit else { return }
        focus = nil
        task = Task {
            if await model.submit() { await onboarding.signedIn(model.mode) }
        }
    }
}

/// One 64 pt row of the field card: label, 4 pt gap, input; focus or error draws the 2 pt accent ring (D4 §1.2).
private struct SignInRow<Content: View>: View {
    @Environment(\.palette) private var p
    @Environment(\.dynamicTypeSize) private var typeSize
    @Environment(\.colorSchemeContrast) private var contrast
    let label: String
    let focused: Bool
    let errored: Bool
    @ViewBuilder let content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(label).font(.inboxBody(13.5, medium: true, relativeTo: .footnote)).foregroundStyle(focused ? p.accentText : p.secondary)
                .lineLimit(typeSize >= .accessibility3 ? 2 : 1).accessibilityHidden(true)
            content
        }
        .padding(.horizontal, 16).padding(.vertical, 10)
        .frame(maxWidth: .infinity, minHeight: 64, alignment: .leading)
        .overlay {
            if focused || errored {
                RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(p.accent, lineWidth: contrast == .increased ? 3 : 2).padding(2)
            }
        }
    }
}

/// Lock-out / unreachable banner: card fill, status colour on the icon only, action on its own line at accessibility sizes.
private struct SignInBanner: View {
    @Environment(\.palette) private var p
    @Environment(\.dynamicTypeSize) private var typeSize
    let icon: String
    let lead: String
    let message: String
    let spoken: String
    var action: (String, () -> Void)?

    private var actionButton: some View {
        Button { action?.1() } label: {
            Text(action?.0 ?? "").font(.inboxBody(15, medium: true)).foregroundStyle(p.accentText).frame(minHeight: 44)
        }.buttonStyle(.plain)
    }

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: icon).font(.system(size: 18)).foregroundStyle(p.warning).padding(.top, 1).accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 0) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(lead).font(.inboxBody(15, medium: true)).foregroundStyle(p.text)
                    Text(message).font(.inboxBody(14, relativeTo: .subheadline)).foregroundStyle(p.secondary).monospacedDigit()
                }
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityElement(children: .ignore).accessibilityLabel(spoken)
                if action != nil, typeSize.isAccessibilitySize { actionButton }
            }
            Spacer(minLength: 8)
            if action != nil, !typeSize.isAccessibilitySize { actionButton }
        }
        .padding(.horizontal, 14).padding(.vertical, 12)
        .background(RoundedRectangle(cornerRadius: 12, style: .continuous).fill(p.card))
    }
}
