import Testing
import Foundation
@testable import API

@Suite struct SSETests {
    @Test func parserHandlesServerFraming() {
        var p = SSEParser()
        var frames: [SSEParser.Frame] = []
        for line in ["retry: 3000", ": connected", "", "id: 1", "event: mail.new", #"data: {"type":"mail.new","message_id":"m"}"#, "", ": ping", ""] {
            if let f = p.feed(line: line) { frames.append(f) }
        }
        #expect(frames.count == 1)
        #expect(frames[0].id == "1" && frames[0].event == "mail.new")
        #expect(p.retry == 3)
    }

    @Test func parserJoinsMultilineData() {
        var p = SSEParser()
        _ = p.feed(line: "data: a"); _ = p.feed(line: "data:b")
        #expect(p.feed(line: "")?.data == "a\nb")
    }

    @Test func envelopeDecoding() {
        let id = UUID()
        let ev = ServerEvent.decode(Data(#"{"type":"mail.new","message_id":"m1","business_id":"\#(id.uuidString.lowercased())","route":"routed","state":"new","received_at":"2026-10-01T10:00:00.5Z"}"#.utf8))
        guard case .mailNew(let m)? = ev else { Issue.record("not mail.new"); return }
        #expect(m.messageId == "m1" && m.businessId == id && m.route == "routed" && m.receivedAt != nil)
        if case .unknown(let t, _)? = ServerEvent.decode(Data(#"{"type":"future.thing","x":1}"#.utf8)) { #expect(t == "future.thing") }
        else { Issue.record("expected unknown") }
        if case .healthChanged(let f)? = ServerEvent.decode(Data(#"{"type":"health.changed","ok":true}"#.utf8)) { #expect(f["ok"] == .bool(true)) }
        else { Issue.record("expected health") }
        #expect(ServerEvent.decode(Data("not json".utf8)) == nil)
        #expect(ServerEvent.decode(Data(#"{"no":"type"}"#.utf8)) == nil)
    }

    @Test func streamYieldsEventsAndReconnects() async throws {
        let calls = Mutex(0)
        let server = MockServer { req in
            #expect(req.headers["Accept"] == "text/event-stream")
            #expect(req.headers["Authorization"] == "Bearer tok")
            let n = calls.withLock { $0 += 1; return $0 }
            let body = "retry: 3000\n: connected\n\nid: \(n)\nevent: mail.new\ndata: {\"type\":\"mail.new\",\"message_id\":\"m\(n)\"}\n\n"
            return (200, ["Content-Type": "text/event-stream"], Data(body.utf8))
        }
        let sse = SSEClient(api: server.client(), minBackoff: 0.01, maxBackoff: 0.02)
        var got: [SSEClient.Update] = []
        for await u in sse.events() {
            got.append(u)
            if got.count == 3 { break }
        }
        guard case .event(.mailNew(let a)) = got[0], a.messageId == "m1" else { Issue.record("first \(got)"); return }
        #expect(got[1] == .reconnected)
        guard case .event(.mailNew(let b)) = got[2], b.messageId == "m2" else { Issue.record("third \(got)"); return }
    }

    @Test func unauthorizedStopsStream() async {
        let server = MockServer { _ in json(#"{"type":"about:blank","title":"Unauthorized","status":401,"code":"unauthorized"}"#, status: 401) }
        let sse = SSEClient(api: server.client(), minBackoff: 0.01)
        var got: [SSEClient.Update] = []
        for await u in sse.events() { got.append(u) }
        #expect(got == [.unauthorized])
    }
}
