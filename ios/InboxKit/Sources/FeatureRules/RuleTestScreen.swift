import SwiftUI
import Observation
import API
import DesignSystem
import Domain
import Persistence

@MainActor @Observable
public final class RuleTestModel {
    public enum Phase { case loading, loaded(RuleTestResult), failed(String), invalid(String) }
    public private(set) var phase: Phase = .loading
    public private(set) var businessNames: [UUID: String] = [:]
    private let env: AppEnvironment
    public let draft: RuleDraft
    public let scope: RuleTestScope

    public init(env: AppEnvironment, draft: RuleDraft, scope: RuleTestScope = .all) { self.env = env; self.draft = draft; self.scope = scope }

    public func run() async {
        phase = .loading
        businessNames = Dictionary(uniqueKeysWithValues: ((try? await env.database.businesses()) ?? []).map { ($0.id, $0.name) })
        do { phase = .loaded(try await env.rules.test(draft, scope: scope)) }
        catch let e as APIError {
            if let f = e.ruleFieldErrors.first { phase = .invalid(f.message) }
            else { phase = .failed(e.isRetryable ? "Couldn't reach the server." : "The test couldn't run.") }
        } catch { phase = .failed("The test couldn't run.") }
    }

    public func who(_ m: RuleMessageRef) -> String { m.fromName ?? m.fromAddr ?? "Unknown sender" }
    public func business(_ m: RuleMessageRef) -> String? { m.businessId.flatMap { businessNames[$0] } }
}

/// RU08 (results) and RU09 (nothing would match): which recent messages the draft would catch, and which an earlier rule keeps.
public struct RuleTestScreen: View {
    @Environment(\.palette) private var p
    @State private var model: RuleTestModel

    public init(env: AppEnvironment, draft: RuleDraft, scope: RuleTestScope = .all) {
        _model = State(initialValue: RuleTestModel(env: env, draft: draft, scope: scope))
    }

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                switch model.phase {
                case .loading:
                    VStack(spacing: 8) { ProgressView(); Text("Testing…").font(.inboxBody(16)).foregroundStyle(p.secondary) }
                        .frame(maxWidth: .infinity).padding(.top, 48)
                case .invalid(let msg): Banner(.error, title: "Fix the pattern first", message: msg)
                case .failed(let msg): Banner(.warning, title: msg, actionTitle: "Try again") { Task { await model.run() } }
                case .loaded(let r): results(r)
                }
            }
            .padding(16)
        }
        .background(p.ground)
        .inboxPushed(title: model.draft.hasName ? model.draft.name : "Rule test", back: "Rule")
        .task { await model.run() }
    }

    @ViewBuilder private func results(_ r: RuleTestResult) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("Test results").font(.inboxHeadingBold(27, relativeTo: .title2)).tracking(-0.5).foregroundStyle(p.text)
            Text("\(r.wouldMatch.count) of the last \(r.scanned) messages").font(.inboxBody(15)).foregroundStyle(p.secondary)
        }
        .accessibilityElement(children: .combine).accessibilityAddTraits(.isHeader)
        if r.wouldMatch.isEmpty && r.caughtEarlier.isEmpty {
            EmptyState(title: "No matches", message: "None of the last \(r.scanned) messages match. Check the address or widen the subject.")
        }
        if !r.wouldMatch.isEmpty {
            InsetGroup("Would match · \(r.wouldMatch.count)") {
                ForEach(Array(r.wouldMatch.enumerated()), id: \.element.id) { i, m in
                    line(m, divider: i < r.wouldMatch.count - 1, note: nil)
                }
            }
        }
        if !r.caughtEarlier.isEmpty {
            InsetGroup("Caught earlier by another rule · \(r.caughtEarlier.count)") {
                ForEach(Array(r.caughtEarlier.enumerated()), id: \.element.id) { i, e in
                    line(e.message, divider: i < r.caughtEarlier.count - 1,
                         note: "Matched “\(e.byRule.name)” first. Move \(model.draft.hasName ? model.draft.name : "this rule") above it to win.")
                }
            }
        }
    }

    private func line(_ m: RuleMessageRef, divider: Bool, note: String?) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack {
                Text(model.business(m) ?? model.who(m)).font(.inboxBody(17, medium: true)).foregroundStyle(p.text).lineLimit(1)
                Spacer()
                Text(RuleFormat.when(m.receivedAt)).font(.inboxBody(14)).foregroundStyle(p.secondary)
            }
            Text(m.subject ?? "(no subject)").font(.inboxBody(16)).foregroundStyle(p.text).lineLimit(1)
            if let to = m.toLocal { Text("to \(to)@").font(.inboxBody(14)).foregroundStyle(p.secondary) }
            if let note { Text(note).font(.inboxBody(14)).foregroundStyle(p.secondary).padding(.top, 2) }
        }
        .padding(.horizontal, 16).padding(.vertical, 10).frame(maxWidth: .infinity, alignment: .leading)
        .overlay(alignment: .bottom) { if divider { Rectangle().fill(p.hairline).frame(height: 0.5).padding(.leading, 16) } }
        .accessibilityElement(children: .combine)
    }
}
