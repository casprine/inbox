import Testing
import Foundation
import API
import Persistence
import Domain
@testable import FeatureSettings
@testable import FeatureBusinesses
import FeatureOnboarding

/// Records what would be handed to Alfather over the LAN.
private final class RecordingDiscovery: AlfatherDiscovering, @unchecked Sendable {
    let found: Bool
    var delivered: [(AlfatherPairingGrant, AlfatherService)] = []
    init(found: Bool) { self.found = found }
    func discover(timeout: Duration) async -> AlfatherService? { found ? AlfatherService(name: "alfather", host: "alfather.local") : nil }
    func deliver(_ grant: AlfatherPairingGrant, to service: AlfatherService) async throws { delivered.append((grant, service)) }
}

@MainActor @Suite struct BusinessEditTests {
    private func rig(_ handler: @escaping MockURLProtocol.Handler) async throws -> (AppEnvironment, MockServer) {
        let server = MockServer(handler)
        keep.append(server)
        let env = AppEnvironment(api: server.client(), tokenStore: InMemoryTokenStore("t"), database: try .inMemory(), reachability: ManualReachability())
        try await MockData.seed(env.database)
        return (env, server)
    }
    nonisolated private static func oneJSON(_ id: UUID, hidden: Bool) -> String {
        #"{"id":"\#(id.uuidString.lowercased())","name":"B","domain":"b.co","sort_order":0,"hidden":\#(hidden),"enabled":true,"routing":"ok","unread":0,"last_message_at":null,"addresses":[]}"#
    }
    nonisolated private static func listJSON(_ order: [UUID], hidden: Set<UUID> = []) -> String {
        let items = order.enumerated().map { i, id in
            #"{"id":"\#(id.uuidString.lowercased())","name":"B\#(i)","domain":"b\#(i).co","sort_order":\#(i),"hidden":\#(hidden.contains(id)),"enabled":true,"routing":"ok","unread":0,"last_message_at":null,"addresses":[]}"#
        }
        return #"{"businesses":[\#(items.joined(separator: ","))]}"#
    }

    @Test func reorderIsOptimisticAndSendsTheOrder() async throws {
        let ids = [MockData.acme, MockData.northgate, MockData.lumen]
        let (env, server) = try await rig { req in
            req.path == "/v1/businesses:order" ? json(Self.listJSON([MockData.lumen, MockData.acme, MockData.northgate])) : json("{}", status: 404)
        }
        let m = BusinessesSettingsModel(env: env)
        m.observe_once_for_tests(try await env.businesses.all())
        await m.move(from: IndexSet(integer: 2), to: 0)             // drag Lumen to the top
        #expect(try await env.businesses.all().map(\.id) == [MockData.lumen, MockData.acme, MockData.northgate])
        let put = server.requests.last { $0.path == "/v1/businesses:order" }
        #expect(put?.method == "PUT" && (put?.json?["ids"] as? [String])?.first?.lowercased() == MockData.lumen.uuidString.lowercased())
        _ = ids
    }

    @Test func reorderFailureRestoresTheOldOrder() async throws {
        let (env, _) = try await rig { _ in json("{}", status: 500) }
        let before = try await env.businesses.all().map(\.id)
        let m = BusinessesSettingsModel(env: env)
        m.observe_once_for_tests(try await env.businesses.all())
        await m.move(from: IndexSet(integer: 0), to: 3)
        #expect(try await env.businesses.all().map(\.id) == before)
        #expect(m.notice != nil)
    }

    @Test func hideIsOptimisticPatchesHiddenAndRollsBack() async throws {
        let (env, server) = try await rig { req in req.method == "PATCH" ? json(Self.oneJSON(MockData.acme, hidden: true)) : json("{}", status: 404) }
        try await env.businesses.setHidden(true, id: MockData.acme)
        #expect(try await env.businesses.all().first { $0.id == MockData.acme }?.hidden == true)
        let patch = server.requests.last { $0.method == "PATCH" }
        #expect(patch?.path == "/v1/businesses/\(MockData.acme.uuidString.lowercased())" && patch?.json?["hidden"] as? Bool == true)
        let (bad, _) = try await rig { _ in json("{}", status: 500) }
        await #expect(throws: Error.self) { try await bad.businesses.setHidden(true, id: MockData.acme) }
        #expect(try await bad.businesses.all().first { $0.id == MockData.acme }?.hidden == false)
    }

    @Test func voiceOverMoveStepsOneRow() async throws {
        let (env, _) = try await rig { _ in json(Self.listJSON([MockData.northgate, MockData.acme, MockData.lumen])) }
        let m = BusinessesSettingsModel(env: env)
        let rows = try await env.businesses.all()
        m.observe_once_for_tests(rows)
        #expect(!m.canMove(rows[0], by: -1) && m.canMove(rows[0], by: 1) && !m.canMove(rows[2], by: 1))
        await m.move(rows[0], by: 1)
        #expect(try await env.businesses.all().map(\.id) == [MockData.northgate, MockData.acme, MockData.lumen])
    }
}

@MainActor @Suite struct HealthRowsTests {
    @Test func groupsMailArrivingAndOrdersRows() {
        let a = UUID(), b = UUID()
        func c(_ id: String, _ s: HealthCheck.State, _ msg: String, biz: UUID? = nil, fix: String? = nil) -> HealthCheck {
            HealthCheck(id: id, businessId: biz, title: id, state: s, message: msg, fixRoute: fix)
        }
        let report = HealthReport(state: .needsYou, checkedAt: .now, checks: [
            c("alfather", .off, "Not paired"), c("mail_arriving", .ok, "Mail is arriving", biz: a),
            c("queue", .ok, "Nothing waiting"), c("mail_arriving", .warn, "kettle.house quiet for 6 days", biz: b, fix: "business/\(b.uuidString.lowercased())/routing"),
            c("gmail", .error, "Signed out", fix: "gmail"), c("worker", .ok, "Running")])
        let env = try! AppEnvironment.preview()
        let m = HealthModel(env: env, report: report)
        #expect(m.rows.map(\.id) == ["mail_arriving", "worker", "gmail", "queue", "alfather"])
        #expect(m.rows[0].title == "Mail arriving" && m.rows[0].state == .warn && m.rows[0].message == "kettle.house quiet for 6 days")
        #expect(m.rows[0].businessId == b && m.rows[0].fixRoute?.hasSuffix("/routing") == true)
        let ok = HealthModel(env: env, report: HealthReport(state: .ok, checkedAt: .now, checks: [c("mail_arriving", .ok, "x", biz: a), c("mail_arriving", .ok, "x", biz: b)]))
        #expect(ok.rows.count == 1 && ok.rows[0].message == "Mail is arriving for all 2 businesses" && ok.rows[0].state == .ok)
    }

    @Test func relativeDays() {
        var cal = Calendar(identifier: .gregorian); cal.timeZone = TimeZone(identifier: "UTC")!
        let loc = Locale(identifier: "en_US")
        let now = ISO8601DateFormatter().date(from: "2026-10-12T12:00:00Z")!
        func s(_ iso: String) -> String { RelativeDay.string(ISO8601DateFormatter().date(from: iso)!, now: now, calendar: cal, locale: loc).replacingOccurrences(of: "\u{202F}", with: " ") }
        #expect(s("2026-10-12T09:41:00Z") == "today, 9:41 AM")
        #expect(s("2026-10-11T18:40:00Z") == "yesterday, 6:40 PM")
        #expect(s("2026-10-08T07:05:00Z") == "Thursday, 7:05 AM")
        #expect(s("2026-10-05T12:00:00Z") == "Oct 5, 12:00 PM")
        #expect(s("2025-12-24T12:00:00Z") == "Dec 24, 2025, 12:00 PM")
    }
}

@MainActor @Suite struct AlfatherTests {
    @Test func statusStatesFollowPairedConnectedAndLastSeen() throws {
        func status(_ json: String) throws -> AlfatherStatus { try APIClient.makeDecoder().decode(AlfatherStatus.self, from: Data(json.utf8)) }
        #expect(try status(#"{"paired":false,"connected":false,"paired_at":null,"last_seen_at":null,"waiting":0,"cursor_seq":0,"mcp_url":null}"#).state() == .notPaired)
        #expect(try status(#"{"paired":true,"connected":true,"paired_at":"2026-10-01T09:00:00Z","last_seen_at":"2026-10-11T09:00:00Z","waiting":0,"mcp_url":"https://h/mcp"}"#).state() == .connected)
        let quiet = try status(#"{"paired":true,"connected":false,"paired_at":"2026-10-01T09:00:00Z","last_seen_at":"2026-10-11T09:00:00Z","waiting":3}"#)
        let seen = quiet.lastSeen!
        #expect(quiet.state(now: seen.addingTimeInterval(600)) == .reconnecting)
        #expect(quiet.state(now: seen.addingTimeInterval(3_600)) == .unreachable && quiet.waiting == 3)
    }

    @Test func pairingMintsATokenAndHandsItToAlfatherOverTheLAN() async throws {
        let server = MockServer { req in
            switch (req.method, req.path) {
            case ("POST", "/v1/alfather/pairings"): json(#"{"consumer_id":"c1","mcp_url":"https://inbox.example.com/mcp","token":"alf_secret","scope":"mail.read","include_existing":false}"#, status: 201)
            case ("GET", "/v1/alfather/status"): json(#"{"paired":true,"connected":true,"paired_at":"2026-10-11T09:00:00Z","last_seen_at":"2026-10-11T09:00:00Z","waiting":0,"mcp_url":"https://inbox.example.com/mcp"}"#)
            default: json("{}", status: 404)
            }
        }
        keep.append(server)
        let env = AppEnvironment(api: server.client(), tokenStore: InMemoryTokenStore("t"), database: try .inMemory(), reachability: ManualReachability())
        let lan = RecordingDiscovery(found: true)
        let m = AlfatherModel(env: env, discovery: lan)
        await m.pairNow()
        #expect(lan.delivered.count == 1 && lan.delivered[0].0.token == "alf_secret" && lan.delivered[0].0.mcpUrl == "https://inbox.example.com/mcp")
        #expect(m.state == .connected && m.error == nil)
        let bodyJSON = server.requests.first { $0.path == "/v1/alfather/pairings" }?.json
        #expect(bodyJSON?["include_existing"] as? Bool == false)
    }

    @Test func unpairCallsDelete() async throws {
        let server = MockServer { req in
            req.method == "DELETE" ? (204, [:], Data()) : json(#"{"paired":false,"connected":false,"paired_at":null,"last_seen_at":null,"waiting":0,"mcp_url":null}"#)
        }
        keep.append(server)
        let env = AppEnvironment(api: server.client(), tokenStore: InMemoryTokenStore("t"), database: try .inMemory(), reachability: ManualReachability())
        let m = AlfatherModel(env: env, discovery: RecordingDiscovery(found: false))
        await m.unpair()
        #expect(server.requests.contains { $0.method == "DELETE" && $0.path == "/v1/alfather" } && m.state == .notPaired)
    }
}

@MainActor @Suite struct SettingsTests {
    @Test func healthRoutesMapFixRoutes() {
        let id = UUID()
        #expect(HealthModel.route(for: "gmail") == .gmail)
        #expect(HealthModel.route(for: "alfather") == .alfather)
        #expect(HealthModel.route(for: "business/\(id.uuidString.lowercased())/routing") == .routing(id))
        #expect(HealthModel.route(for: nil) == nil && HealthModel.route(for: "nonsense") == nil)
    }

    @Test func healthHeadlineCountsOnlyWarnAndError() async throws {
        let env = try AppEnvironment.preview()
        let m = HealthModel(env: env)
        await m.load()
        #expect(m.report != nil && m.report!.state == .needsYou)
        #expect(m.report!.problems.count == m.report!.checks.filter { $0.state == .warn || $0.state == .error }.count)
        #expect(m.headline == "\(m.report!.problems.count) things need you" && m.rowValue == "Needs you")
        let off = HealthReport(state: .ok, checkedAt: .now, checks: [HealthCheck(id: "alfather", title: "Alfather", state: .off, message: "Not set up")])
        #expect(off.problems.isEmpty)
    }

    @Test func alfatherStatusMissingEndpointMeansNotPaired() async throws {
        let server = MockServer { _ in json("{}", status: 404) }
        keep.append(server)
        let env = AppEnvironment(api: server.client(), tokenStore: InMemoryTokenStore("t"), database: try .inMemory(), reachability: ManualReachability())
        let m = AlfatherModel(env: env, discovery: RecordingDiscovery(found: false))
        await m.load()
        #expect(m.state == .notPaired && m.error == nil)
        await m.pairNow()
        #expect(m.error?.contains("wasn't found") == true)
    }

    @Test func themePreferencePersists() async throws {
        let db = try AppDatabase.inMemory()
        let t = ThemePreference()
        await t.set("dark", db)
        await t.setPreviewLines(3, db)
        let again = ThemePreference()
        await again.load(db)
        #expect(again.raw == "dark" && again.previewLines == 3)
    }

    @Test func businessReorderHideAndRemove() async throws {
        let env = try AppEnvironment.preview()
        try await MockData.seed(env.database)
        let all = try await env.businesses.all()
        #expect(all.count == 3)
        let m = BusinessesSettingsModel(env: env)
        m.observe_once_for_tests(all)
        #expect(BusinessesSettingsModel.routingText("to_email").ok == false && BusinessesSettingsModel.routingText("ok").ok)
    }

    @Test func routingProblemChecksAgain() async throws {
        let env = try AppEnvironment.preview()
        try await MockData.seed(env.database)
        let lumen = MockData.lumen
        let m = RoutingProblemModel(env: env, id: lumen)
        await m.load()
        await m.checkAgain()
        #expect(m.isFixed && m.error == nil)
    }

    @Test func addBusinessListsOnlySelectableChoices() async throws {
        let env = try AppEnvironment.preview()
        let m = AddBusinessModel(env: env)
        await m.load()
        #expect(m.domains.count == 5 && m.chosen?.domain == "acme-studio.com")
        let kettle = m.domains.first { $0.domain == "kettle.house" }!
        m.select(kettle)
        #expect(m.chosen?.domain == "acme-studio.com")   // not selectable: ignored
    }
}

@MainActor private var keep: [MockServer] = []
