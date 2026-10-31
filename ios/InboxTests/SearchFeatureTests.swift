import Testing
import Foundation
import API
import Persistence
import Domain
@testable import FeatureSearch

private func body(_ q: String) -> String {
    #"""
    {"groups":[{"business":{"id":"00000000-0000-4000-8000-0000000000a1","name":"Acme Studio","domain":"acme.test"},"total":1,
    "messages":[{"id":"00000000-0000-4000-8000-000000000001","business_id":"00000000-0000-4000-8000-0000000000a1","from_addr":"n@x.test",
    "from_name":"Northgate Ltd","subject":"Invoice #1","snippet":"An invoice for you","received_at":"2026-10-01T10:00:00Z","is_read":false,
    "archived":false,"attachment_count":1,"thread_key":"t","highlights":{"subject":[{"start":0,"end":7}],"from":[],"snippet":[{"start":3,"end":10}]}}]}],
    "total":1,"parsed":{"words":["\#(q)"],"phrases":[],"prefix":"","from":[],"to":[],"has_attachment":false,"terms":["\#(q)"]}}
    """#
}

@MainActor
private struct Rig {
    let server: MockServer, model: SearchModel, db: AppDatabase
    init(debounce: Duration = .milliseconds(40), scope: UUID? = nil, delay: TimeInterval = 0,
         _ handler: (@Sendable (CapturedRequest) -> (status: Int, headers: [String: String], body: Data))? = nil) throws {
        let store = Mutex<[String]>([])
        server = MockServer { req in
            if delay > 0 { Thread.sleep(forTimeInterval: delay) }
            if req.path == "/v1/search/recents" {
                switch req.method {
                case "POST": let q = req.json?["q"] as? String ?? ""; store.withLock { $0.insert(q, at: 0) }
                case "DELETE": store.withLock { $0 = [] }
                default: break
                }
                let list = store.withLock { $0 }.map { "\"\($0)\"" }.joined(separator: ",")
                return json(#"{"recents":[\#(list)]}"#)
            }
            if let handler { return handler(req) }
            let q = URLComponents(url: req.url, resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "q" }?.value ?? ""
            return json(body(q))
        }
        db = try .inMemory()
        model = SearchModel(backend: APISearchBackend(api: server.client(), db: db), scope: scope, debounce: debounce)
    }
    var searches: [CapturedRequest] { server.requests.filter { $0.path == "/v1/search" } }
}

@MainActor @Suite struct SearchModelTests {
    @Test func debounceCollapsesTypingIntoOneRequest() async throws {
        let r = try Rig()
        r.model.query = "i"; r.model.query = "in"; r.model.query = "inv"
        await r.model.settle()
        #expect(r.searches.count == 1)
        #expect(r.searches.first?.url.query?.contains("q=inv") == true)
        #expect(r.model.phase == .results)
        #expect(r.model.sections.first?.name == "Acme Studio")
        #expect(r.model.sections.first?.hits.first?.subjectSpans == [Span(start: 0, end: 7)])
    }

    @Test func clearingTheQueryCancelsInFlightAndReturnsToIdle() async throws {
        let r = try Rig(debounce: .milliseconds(1), delay: 0.3)
        r.model.query = "invoice"
        try await Task.sleep(for: .milliseconds(100))
        r.model.query = ""
        await r.model.settle()
        try await Task.sleep(for: .milliseconds(400))
        #expect(r.model.phase == .idle && r.model.sections.isEmpty)
    }

    @Test func staleResponseNeverOverwritesNewerQuery() async throws {
        let r = try Rig(debounce: .milliseconds(1)) { req in
            let q = URLComponents(url: req.url, resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "q" }?.value ?? ""
            if q == "slow" { Thread.sleep(forTimeInterval: 0.3); return json(body("slow")) }
            return json(#"{"groups":[],"total":0,"parsed":{"words":[],"phrases":[],"prefix":"","from":[],"to":[],"has_attachment":false,"terms":[]}}"#)
        }
        r.model.query = "slow"
        try await Task.sleep(for: .milliseconds(80))
        r.model.query = "fast"
        await r.model.settle()
        try await Task.sleep(for: .milliseconds(500))
        #expect(r.model.phase == .none && r.model.sections.isEmpty)
    }

    @Test func scopeIsSentAndWidening() async throws {
        let r = try Rig(scope: MockData.acme)
        r.model.query = "invoice"; await r.model.settle()
        #expect(r.searches.last?.url.query?.contains("business=") == true)
        r.model.widenScope(); await r.model.settle()
        #expect(r.model.scope == nil && r.model.showsScopeChip == false)
        #expect(r.searches.count == 2 && r.searches.last?.url.query?.contains("business=") == false)
    }

    @Test func operatorInsertion() throws {
        let r = try Rig()
        #expect(r.model.insert(.from) == "from:")
        r.model.query = "invoice"
        #expect(r.model.insert(.hasAttachment) == "invoice has:attachment ")
        r.model.query = "invoice "
        #expect(r.model.insert(.to) == "invoice to:")
    }

    @Test func submitAndOpenSaveRecents() async throws {
        let r = try Rig()
        r.model.query = "northgate"
        await r.model.submit()
        #expect(r.model.recents == ["northgate"])
        r.model.query = "invoice"; await r.model.settle()
        await r.model.opened()
        #expect(r.model.recents == ["invoice", "northgate"])
        #expect(try await r.db.recentSearches() == ["invoice", "northgate"])
        await r.model.clearRecents()
        #expect(r.model.recents.isEmpty)
        #expect(try await r.db.recentSearches().isEmpty)
    }

    @Test func noResultsAndImportingFlag() async throws {
        let r = try Rig { _ in json(#"{"groups":[],"total":0,"parsed":{"words":["zzz"],"phrases":[],"prefix":"","from":[],"to":[],"has_attachment":false,"terms":["zzz"]}}"#) }
        r.model.importing = true
        r.model.query = "zzz"; await r.model.settle()
        #expect(r.model.phase == .none && r.model.importing)
    }

    @Test func failureShowsFailedAndEmptyQueryIsQuiet() async throws {
        let r = try Rig { _ in json(#"{"type":"x","title":"t","status":400,"code":"empty_query"}"#, status: 400, headers: ["Content-Type": "application/problem+json"]) }
        r.model.query = "from:"; await r.model.settle()
        #expect(r.model.phase == .none)
        let f = try Rig { _ in json("{}", status: 500) }
        f.model.query = "x"; await f.model.settle()
        #expect(f.model.phase == .failed)
    }
}

@MainActor @Suite struct LocalSearchTests {
    @Test func mockModeSearchesSeededData() async throws {
        let db = try AppDatabase.inMemory()
        try await MockData.seed(db)
        let b = LocalSearchBackend(db: db)
        let all = try await b.search("invoice", business: nil)
        #expect(all.sections.map(\.name).first == "Acme Studio")
        #expect(all.sections.count >= 2 && all.hitCount >= 3)
        #expect(all.sections.first?.hits.first?.subjectSpans.isEmpty == false || all.sections.first?.hits.first?.snippetSpans.isEmpty == false)
        let scoped = try await b.search("invoice", business: MockData.acme)
        #expect(scoped.sections.count == 1)
        let from = try await b.search("from:lumen has:attachment", business: nil)
        #expect(from.hitCount == 0 || from.sections.allSatisfy { $0.hits.allSatisfy { $0.attachmentCount > 0 } })
        #expect(try await b.search("zzzzqq", business: nil).hitCount == 0)
    }

    @Test func highlightSpansAndSegments() {
        #expect(SearchHighlight.spans(in: "Your Invoice is ready, invoices", terms: ["invoice"]) == [Span(start: 5, end: 12), Span(start: 23, end: 30)])
        let segs = SearchHighlight.segments("héllo wörld", spans: [Span(start: 6, end: 11)])
        #expect(segs.map(\.text) == ["héllo ", "wörld"] && segs.map(\.highlighted) == [false, true])
        let q = SearchQuery("from:stripe \"big sale\" has:attachment inv")
        #expect(q.from == ["stripe"] && q.phrases == ["big sale"] && q.hasAttachment && q.words == ["inv"])
    }
}

// MARK: Pagination (B-4: `limit`, `cursor`, `has_more`, `next_cursor`)

private func page(ids: [Int], business: String = "00000000-0000-4000-8000-0000000000a1", more: String?) -> String {
    let msgs = ids.map { n in
        #"{"id":"00000000-0000-4000-8000-0000000000\#(String(format: "%02d", n))","business_id":"\#(business)","from_addr":"n@x.test","from_name":"N","subject":"Invoice \#(n)","snippet":"s","received_at":"2026-10-01T10:00:00Z","is_read":false,"archived":false,"attachment_count":0,"thread_key":"t\#(n)","highlights":{"subject":[],"from":[],"snippet":[]}}"#
    }.joined(separator: ",")
    let next = more.map { "\"\($0)\"" } ?? "null"
    return #"{"groups":[{"business":{"id":"\#(business)","name":"Acme Studio","domain":"acme.test"},"total":\#(ids.count),"messages":[\#(msgs)]}],"total":\#(ids.count),"has_more":\#(more != nil),"next_cursor":\#(next),"parsed":{"words":["invoice"],"phrases":[],"prefix":"","from":[],"to":[],"has_attachment":false,"terms":["invoice"]}}"#
}

@MainActor @Suite struct SearchPaginationTests {
    @Test func loadMoreAppendsTheNextPageAndStopsOnTheLast() async throws {
        let r = try Rig(debounce: .milliseconds(1)) { req in
            let cursor = URLComponents(url: req.url, resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "cursor" }?.value
            switch cursor {
            case nil: return json(page(ids: [1, 2], more: "c1"))
            case "c1": return json(page(ids: [3, 4], more: "c2"))
            default: return json(page(ids: [5], more: nil))
            }
        }
        r.model.query = "invoice"
        await r.model.settle()
        #expect(r.model.canLoadMore && r.model.resultCount == 2)
        #expect(r.searches[0].url.query?.contains("limit=50") == true && r.searches[0].url.query?.contains("cursor") == false)
        await r.model.loadMore()
        #expect(r.model.resultCount == 4 && r.model.sections.count == 1 && r.model.sections[0].total == 4 && r.model.canLoadMore)
        await r.model.loadMore()
        #expect(r.model.resultCount == 5 && !r.model.canLoadMore)
        #expect(r.searches.compactMap { URLComponents(url: $0.url, resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "cursor" }?.value } == ["c1", "c2"])
        await r.model.loadMore()                      // no cursor left: no request
        #expect(r.searches.count == 3)
    }

    @Test func duplicatesAreDroppedAndNewBusinessesAreAppended() async throws {
        let other = "00000000-0000-4000-8000-0000000000b2"
        let r = try Rig(debounce: .milliseconds(1)) { req in
            let cursor = URLComponents(url: req.url, resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "cursor" }?.value
            return json(cursor == nil ? page(ids: [1, 2], more: "c1") : page(ids: [9, 10], business: other, more: nil))
        }
        r.model.query = "invoice"
        await r.model.settle()
        await r.model.loadMore()
        #expect(r.model.sections.count == 2 && r.model.sections[1].id.lowercased() == other && r.model.resultCount == 4)
    }

    @Test func aMessageSeenOnTwoPagesIsShownOnce() async throws {
        let r = try Rig(debounce: .milliseconds(1)) { req in
            let cursor = URLComponents(url: req.url, resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "cursor" }?.value
            return json(cursor == nil ? page(ids: [1, 2], more: "c1") : page(ids: [2, 3], more: nil))
        }
        r.model.query = "invoice"
        await r.model.settle()
        await r.model.loadMore()
        #expect(r.model.resultCount == 3 && r.model.sections.count == 1)
    }

    @Test func loadMoreFailureIsRetryableAndANewQueryResetsPaging() async throws {
        let failing = Mutex<Bool>(true)
        let r = try Rig(debounce: .milliseconds(1)) { req in
            let cursor = URLComponents(url: req.url, resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "cursor" }?.value
            if cursor == nil { return json(page(ids: [1], more: "c1")) }
            return failing.withLock { $0 } ? json("{}", status: 500) : json(page(ids: [2], more: nil))
        }
        r.model.query = "invoice"
        await r.model.settle()
        await r.model.loadMore()
        #expect(r.model.loadMoreFailed && r.model.resultCount == 1 && r.model.canLoadMore)
        failing.withLock { $0 = false }
        await r.model.loadMore()
        #expect(!r.model.loadMoreFailed && r.model.resultCount == 2 && !r.model.canLoadMore)
        r.model.query = ""
        #expect(r.model.nextCursor == nil && !r.model.canLoadMore)
    }
}
