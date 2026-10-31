import Testing
import Foundation
@testable import API

@Suite struct APIClientTests {
    @Test func decodesBusinessesAndSendsBearerNoIdempotencyOnGet() async throws {
        let bid = UUID()
        let server = MockServer { _ in json("""
        {"businesses":[{"id":"\(bid.uuidString.lowercased())","name":"Northgate","domain":"northgate.co","sort_order":2,"hidden":false,
        "enabled":true,"routing":"ok","unread":3,"last_message_at":"2026-10-01T10:00:00Z",
        "addresses":[{"local_part":"sales","count":4,"last_at":null}]}]}
        """) }
        let list = try await server.client().businesses()
        #expect(list.count == 1 && list[0].id == bid && list[0].sortOrder == 2 && list[0].addresses[0].localPart == "sales")
        #expect(list[0].lastMessageAt != nil)
        let r = server.requests[0]
        #expect(r.headers["Authorization"] == "Bearer tok")
        #expect(r.headers["Idempotency-Key"] == nil)
        #expect(r.path == "/v1/businesses")
    }

    @Test func fractionalAndPlainDatesBothParse() {
        #expect(APIClient.parseDate("2026-10-01T10:00:00.5Z") != nil)
        #expect(APIClient.parseDate("2026-10-01T10:00:00.123456789Z") != nil)
        #expect(APIClient.parseDate("2026-10-01T10:00:00+02:00") != nil)
    }

    @Test func mutationsCarryIdempotencyKeyAndSnakeCaseBody() async throws {
        let id = UUID()
        let server = MockServer { _ in json(#"{"messages":[\#(messageJSON(id: id, archived: true))]}"#) }
        let msgs = try await server.client().batch(ids: [id], op: .archive, idempotencyKey: "key-1")
        #expect(msgs[0].archived)
        let r = server.requests[0]
        #expect(r.method == "POST" && r.path == "/v1/messages:batch")
        #expect(r.headers["Idempotency-Key"] == "key-1")
        #expect(r.json?["op"] as? String == "archive")
        #expect((r.json?["ids"] as? [String])?.map { $0.lowercased() } == [id.uuidString.lowercased()])
    }

    @Test func generatedKeyWhenNoneSupplied() async throws {
        let server = MockServer { _ in json(#"{"recents":["a"]}"#) }
        _ = try await server.client().addRecentSearch("a")
        #expect(server.requests[0].headers["Idempotency-Key"]?.isEmpty == false)
        #expect(server.requests[0].json?["q"] as? String == "a")
    }

    @Test func problemJSONMapsToAPIErrorWithCode() async {
        let server = MockServer { _ in
            json(#"{"type":"about:blank","title":"Too Many Requests","status":429,"code":"rate_limited","detail":"slow down"}"#,
                 status: 429, headers: ["Content-Type": "application/problem+json", "Retry-After": "7"])
        }
        do {
            _ = try await server.client().attention()
            Issue.record("expected throw")
        } catch let e as APIError {
            #expect(e.code == "rate_limited" && e.status == 429 && e.isRetryable)
            if case .problem(let p, let ra) = e { #expect(p.detail == "slow down"); #expect(ra == 7) }
        } catch { Issue.record("wrong error \(error)") }
    }

    @Test func nonProblemErrorAndTransportErrors() async {
        let s1 = MockServer { _ in (502, [:], Data("bad gateway".utf8)) }
        await #expect(throws: APIError.http(status: 502)) { try await s1.client().attention() }
        let s2 = MockServer { _ in throw URLError(.notConnectedToInternet) }
        do { _ = try await s2.client().attention(); Issue.record("expected throw") }
        catch let e as APIError { #expect(e.isRetryable); if case .transport = e {} else { Issue.record("not transport") } }
        catch { Issue.record("wrong error") }
    }

    @Test func missingTokenFailsBeforeNetwork() async {
        let server = MockServer { _ in json("{}") }
        await #expect(throws: APIError.notAuthenticated) { try await server.client(token: nil).attention() }
        #expect(server.requests.isEmpty)
    }

    @Test func loginStoresTokenSkipsBearerAndSwitchesServer() async throws {
        let did = UUID()
        let server = MockServer { _ in json(#"{"device_id":"\#(did.uuidString.lowercased())","token":"secret"}"#) }
        let store = InMemoryTokenStore()
        let api = APIClient(baseURL: URL(string: "http://placeholder.invalid")!, tokenStore: store, session: server.session)
        let r = try await api.login(server: URL(string: "https://\(server.host)")!, password: "pw", deviceName: "iPhone")
        #expect(r.deviceId == did)
        #expect(await store.token() == "secret")
        #expect(api.baseURL.host == server.host, "later calls go to the server that accepted the password")
        let req = server.requests[0]
        #expect(req.method == "POST" && req.path == "/v1/auth/login" && req.headers["Authorization"] == nil)
        #expect(req.json?["password"] as? String == "pw" && req.json?["device_name"] as? String == "iPhone")
        #expect(req.json?.keys.sorted() == ["device_name", "password"])
    }

    @Test func loginFailuresMapToLoginFailure() async throws {
        func fail(_ h: @escaping MockURLProtocol.Handler) async -> (LoginFailure?, String?) {
            let server = MockServer(h)
            let store = InMemoryTokenStore()
            let api = APIClient(baseURL: server.baseURL, tokenStore: store, session: server.session)
            do { _ = try await api.login(server: URL(string: "https://\(server.host)")!, password: "pw", deviceName: nil); return (nil, await store.token()) }
            catch let e as LoginFailure { return (e, await store.token()) }
            catch { return (nil, "other \(error)") }
        }
        let problem = ["Content-Type": "application/problem+json"]
        let unauthorized = await fail { _ in json(#"{"type":"about:blank","title":"x","status":401,"code":"invalid_credentials"}"#, status: 401, headers: problem) }
        #expect(unauthorized.0 == .invalidCredentials && unauthorized.1 == nil)
        let limited = await fail { _ in json(#"{"type":"about:blank","title":"x","status":429,"code":"rate_limited"}"#, status: 429, headers: problem.merging(["Retry-After": "20"]) { $1 }) }
        #expect(limited.0 == .rateLimited(retryAfter: 20))
        let noHeader = await fail { _ in json(#"{"type":"about:blank","title":"x","status":429,"code":"rate_limited"}"#, status: 429, headers: problem) }
        #expect(noHeader.0 == .rateLimited(retryAfter: 5))
        #expect(await fail { _ in json("{}", status: 500) }.0 == .server)
        #expect(await fail { _ in json("<html/>", status: 404) }.0 == .notInboxServer)
        #expect(await fail { _ in json(#"{"hello":1}"#) }.0 == .notInboxServer)
        #expect(await fail { _ in throw URLError(.cannotFindHost) }.0 == .unreachable)
        #expect(await fail { _ in throw URLError(.secureConnectionFailed) }.0 != nil)
    }

    @Test func messageListQueryAndDetailAndBody() async throws {
        let id = UUID(), biz = UUID()
        let server = MockServer { req in
            if req.path.hasSuffix("/body") {
                return json(#"{"html":"<p>x</p>","text":null,"remote_image_count":2,"auth_results":{"spf":"pass","dkim":"pass","dmarc":"none"},"inline":[{"cid":"c1","attachment_id":null,"mime":"image/png"}]}"#)
            }
            if req.path == "/v1/messages" { return json(#"{"messages":[\#(messageJSON(id: id))],"next_cursor":"abc"}"#) }
            return json("""
            {"message":\(messageJSON(id: id)),"thread":[],"attachments":[{"id":"\(UUID().uuidString.lowercased())","filename":null,"mime":null,"size_bytes":null,"content_id":null,"inline":false}],
             "details":{"business":{"id":"\(biz.uuidString.lowercased())","name":"N","domain":null},"address":"x@y","rule_name":null,"gmail":{"state":"synced","last_error":null},"labels":["a"],"size_bytes":10,"received_at":"2026-10-01T10:00:00Z"}}
            """)
        }
        let api = server.client()
        let page = try await api.messages(.init(business: biz, unread: true, to: "sales", limit: 10, cursor: "c0"))
        #expect(page.nextCursor == "abc")
        let items = URLComponents(url: server.requests[0].url, resolvingAgainstBaseURL: false)!.queryItems!
        #expect(Dictionary(uniqueKeysWithValues: items.map { ($0.name, $0.value!) }) ==
                ["business": biz.uuidString.lowercased(), "unread": "true", "to": "sales", "limit": "10", "cursor": "c0"])
        let d = try await api.message(id: id)
        #expect(d.details.business?.domain == nil && d.details.gmail?.state == "synced" && d.attachments.count == 1)
        let b = try await api.messageBody(id: id)
        #expect(b.remoteImageCount == 2 && b.inline[0].attachmentId == nil && b.authResults.dmarc == "none")
    }

    @Test func attachmentURLReadsLocationWithoutFollowing() async throws {
        let server = MockServer { _ in (302, ["Location": "https://s3.example/obj?sig=1"], Data()) }
        let u = try await server.client().attachmentURL(id: UUID())
        #expect(u.absoluteString == "https://s3.example/obj?sig=1")
    }

    @Test func searchAttentionAndFailedDecode() async throws {
        let id = UUID()
        let server = MockServer { req in
            switch req.path {
            case "/v1/search": return json("""
              {"groups":[{"business":null,"total":1,"messages":[{"id":"\(id.uuidString.lowercased())","business_id":null,"from_addr":null,"from_name":null,"subject":"s","snippet":null,
              "received_at":"2026-10-01T10:00:00Z","is_read":true,"archived":false,"attachment_count":1,"thread_key":"k","highlights":{"subject":[{"start":0,"end":2}],"from":[],"snippet":[]}}]}],
              "total":1,"parsed":{"words":["a"],"phrases":[],"prefix":"a","from":[],"to":[],"has_attachment":true,"terms":["a"]}}
              """)
            case "/v1/attention": return json(#"{"unrouted":{"count":2,"by_business":[{"business_id":null,"name":null,"count":2}]},"failed":{"count":1},"total":3}"#)
            case "/v1/attention/failed":
                let inner = String(messageJSON(id: id).dropFirst().dropLast())
                return json("{\"messages\":[{" + inner + ",\"failure_reason\":\"parse_failed\",\"env_to\":null,\"size_bytes\":5,\"has_raw\":true}]}")
            case "/v1/attention/unrouted": return json(#"{"groups":[],"total":0}"#)
            default: return json(#"{"status":"queued","id":"\#(id.uuidString.lowercased())"}"#, status: 202)
            }
        }
        let api = server.client()
        let s = try await api.search("a has:attachment", business: nil)
        #expect(s.parsed.hasAttachment && s.groups[0].messages[0].highlights.subject[0].end == 2)
        let a = try await api.attention()
        #expect(a.unrouted.byBusiness[0].count == 2 && a.total == 3)
        let f = try await api.failed()
        #expect(f[0].failureReason == "parse_failed" && f[0].hasRaw && f[0].message.id == id)
        #expect(try await api.unrouted().total == 0)
        #expect(try await api.retryFailed(id: id).status == "queued")
        #expect(server.requests.last?.path == "/v1/failed/\(id.uuidString.lowercased()):retry")
        #expect(server.requests.last?.headers["Idempotency-Key"] != nil)
    }
}
