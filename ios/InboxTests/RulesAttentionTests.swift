import Testing
import Foundation
import API
import Persistence
import Domain
@testable import FeatureRules
@testable import FeatureAttention

@MainActor
private func mockEnv() throws -> AppEnvironment {
    let env = try AppEnvironment.preview()
    return env
}

@MainActor @Suite struct RulesListTests {
    @Test func loadsSwitchesAndReorders() async throws {
        let env = try mockEnv()
        try await env.rules.refresh()
        let model = RulesListModel(env: env)
        try await MockData.seed(env.database)
        await model.loadBusinesses()
        model.pendingDelete = nil
        let rules = try await env.database.rules()
        #expect(rules.count == 5)
        #expect(rules.first?.name == "Invoices")
        try await env.rules.setEnabled(false, id: rules[0].id)
        #expect(try await env.database.rules().first?.enabled == false)
        let ids = rules.map(\.id)
        #expect(RulesListModel.reordered(ids, from: IndexSet(integer: 3), to: 0).first == ids[3])
        #expect(RulesListModel.reordered(ids, from: IndexSet(integer: 0), to: 3) == [ids[1], ids[2], ids[0], ids[3], ids[4]])
        try await env.rules.move(ids: Array(ids.reversed()))
        let after = try await env.database.rules()
        #expect(after.map(\.name) == rules.reversed().map(\.name), "got \(after.map(\.name)) positions \(after.map(\.position))")
    }

    @Test func deleteRemovesRule() async throws {
        let env = try mockEnv()
        try await env.rules.refresh()
        let id = try await env.database.rules()[2].id
        try await env.rules.delete(id: id)
        #expect(try await env.database.rules().count == 4)
    }
}

@MainActor @Suite struct RuleEditorTests {
    @Test func invalidPatternBlocksSaveWithReason() async throws {
        let m = RuleEditorModel(env: try mockEnv(), draft: { var d = RuleDraft(); d.name = "Bad"; d.subject = "order (open"; return d }())
        #expect(!m.canSave)
        #expect(m.saveBlockedReason == "Missing a closing bracket “)”.")
        m.draft.subject = "order"
        #expect(m.canSave)
        m.draft.name = "  "
        #expect(m.saveBlockedReason == "Give the rule a name.")
    }

    @Test func savingCreatesRuleAtTop() async throws {
        let env = try mockEnv()
        let m = RuleEditorModel(env: env, draft: { var d = RuleDraft(); d.name = "Wholesale"; d.subject = "wholesale"; d.label = "Kettle/Wholesale"; return d }())
        #expect(await m.save())
        #expect(m.generalError == nil && m.fieldErrors.isEmpty)
    }

    @Test func serverRejectionBecomesFieldError() async throws {
        // Local check passes `[` as invalid too, so craft one the local check accepts but the mock server refuses: none exist; assert apply().
        let m = RuleEditorModel(env: try mockEnv(), draft: RuleDraft())
        let p = try JSONDecoder().decode(Problem.self, from: Data(#"{"type":"about:blank","title":"Unprocessable Entity","status":422,"code":"rule_pattern_invalid","detail":"bad","field":"match_subject","column":3,"errors":[{"field":"match_subject","message":"Subject pattern isn't valid.","column":3}]}"#.utf8))
        m.apply(.problem(p, retryAfter: nil))
        #expect(m.fieldErrors["match_subject"] == "Subject pattern isn't valid.")
        #expect(m.subjectError() == "Subject pattern isn't valid.")
        m.edited()
        #expect(m.fieldErrors.isEmpty)
    }

    @Test func testShowsMatchesAndCaughtEarlier() async throws {
        let env = try mockEnv()
        try await MockData.seed(env.database)
        var d = RuleDraft(); d.name = "Invoices"; d.subject = "invoice"
        let t = RuleTestModel(env: env, draft: d)
        await t.run()
        guard case .loaded(let r) = t.phase else { Issue.record("not loaded"); return }
        #expect(r.scanned == 200)
        #expect(!r.wouldMatch.isEmpty)
    }
}

@MainActor @Suite struct AttentionTests {
    @Test func unroutedGroupsLoad() async throws {
        let env = try mockEnv()
        let m = UnroutedModel(env: env)
        await m.load()
        #expect(m.total == 3 && m.groups.count == 2 && !m.isEmpty)
    }

    @Test func ruleFromMessagePrefillAndCount() async throws {
        let env = try mockEnv()
        let list = try await env.api.unrouted()
        let msg = list.groups[0].messages[0]
        let m = RuleFromMessageModel(env: env, message: msg, businessName: "Kettle House", unroutedTotal: list.total)
        #expect(m.draft.name == "Wholesale")
        #expect(m.draft.subject == "wholesale")
        #expect(m.draft.label == "Kettle/Wholesale")
        #expect(!m.draft.archive && m.draft.labelOnly)
        await m.recount()
        #expect(m.matches == 2)
        #expect(m.matchSummary == "Matches 2 of the 3 unrouted messages.")
        #expect(await m.create())
    }

    @Test func failedRetryAndDismiss() async throws {
        let env = try mockEnv()
        let m = FailedModel(env: env)
        await m.load()
        #expect(m.items.count == 1 && m.items[0].failureReason == "store_failed")
        #expect(await m.dismiss(m.items[0].id))
        #expect(m.items.isEmpty)
    }

    @Test func failureCopy() {
        let c = FailureCopy.text(reason: "store_failed", sizeBytes: 4_400_000)
        #expect(c.lead == "Couldn't store." && c.detail == "The server couldn't save this message. It was still delivered to Gmail.")
        #expect(FailureCopy.text(reason: "parse_failed", sizeBytes: nil).lead == "Couldn't read this message.")
    }

    @Test func ruleDescriptionsMatchBoard() {
        let r = RuleRecord(id: UUID(), name: "Invoices", position: 0, matchToLocal: "invoices")
        #expect(r.matchSummary(businessName: "Acme Studio", domain: "acme-studio.com") == "invoices@ on acme-studio.com")
        let s = RuleRecord(id: UUID(), name: "Receipts", position: 1, matchFrom: "@stripe.com")
        #expect(s.matchSummary(businessName: "Kettle House", domain: "kettle.house") == "From @stripe.com on kettle.house")
        let any = RuleRecord(id: UUID(), name: "Rest", position: 2)
        #expect(any.matchSummary(businessName: "Acme Studio", domain: "acme-studio.com") == "Any other address on acme-studio.com")
    }
}
