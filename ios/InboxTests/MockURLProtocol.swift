import Foundation
import API

/// Routes requests to per-host handlers so tests can run in parallel: each test uses `MockServer.make()`
/// to get a unique host, a URLSession bound to this protocol, and a request log.
final class MockURLProtocol: URLProtocol, @unchecked Sendable {
    typealias Handler = @Sendable (CapturedRequest) throws -> (status: Int, headers: [String: String], body: Data)
    static let handlers = Mutex<[String: Handler]>([:])

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        guard let url = request.url, let host = url.host, let h = Self.handlers.withLock({ $0[host] }) else {
            client?.urlProtocol(self, didFailWithError: URLError(.cannotFindHost)); return
        }
        do {
            let (status, headers, body) = try h(CapturedRequest(request))
            let resp = HTTPURLResponse(url: url, statusCode: status, httpVersion: "HTTP/1.1", headerFields: headers)!
            client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: body)
            client?.urlProtocolDidFinishLoading(self)
        } catch {
            client?.urlProtocol(self, didFailWithError: error)
        }
    }
    override func stopLoading() {}
}

struct CapturedRequest: Sendable {
    let method: String, url: URL, headers: [String: String], body: Data
    var path: String { url.path }
    var json: [String: Any]? { (try? JSONSerialization.jsonObject(with: body)) as? [String: Any] }
    init(_ r: URLRequest) {
        method = r.httpMethod ?? "GET"; url = r.url!; headers = r.allHTTPHeaderFields ?? [:]
        var data = r.httpBody ?? Data()
        if data.isEmpty, let s = r.httpBodyStream {
            s.open(); defer { s.close() }
            var buf = [UInt8](repeating: 0, count: 4096)
            while s.hasBytesAvailable { let n = s.read(&buf, maxLength: buf.count); if n <= 0 { break }; data.append(buf, count: n) }
        }
        body = data
    }
}

final class MockServer: Sendable {
    let host: String
    let session: URLSession
    let baseURL: URL
    private let log = Mutex<[CapturedRequest]>([])
    var requests: [CapturedRequest] { log.withLock { $0 } }

    init(_ handler: @escaping MockURLProtocol.Handler) {
        host = "t-\(UUID().uuidString.prefix(8).lowercased()).test"
        baseURL = URL(string: "http://\(host)")!
        let cfg = URLSessionConfiguration.ephemeral
        cfg.protocolClasses = [MockURLProtocol.self]
        session = URLSession(configuration: cfg)
        let log = self.log
        MockURLProtocol.handlers.withLock { $0[host] = { req in log.withLock { $0.append(req) }; return try handler(req) } }
    }
    deinit { let h = host; MockURLProtocol.handlers.withLock { $0[h] = nil } }

    func client(token: String? = "tok") -> APIClient {
        APIClient(baseURL: baseURL, tokenStore: InMemoryTokenStore(token), session: session)
    }
}

func json(_ s: String, status: Int = 200, headers: [String: String] = [:]) -> (status: Int, headers: [String: String], body: Data) {
    (status, headers.merging(["Content-Type": "application/json"]) { a, _ in a }, Data(s.utf8))
}

func messageJSON(id: UUID, read: Bool = false, archived: Bool = false, subject: String = "Hi", at: String = "2026-10-01T10:00:00.123456Z") -> String {
    """
    {"id":"\(id.uuidString.lowercased())","business_id":null,"direction":"in","from_addr":"a@b.co","from_name":null,"subject":"\(subject)",
     "snippet":null,"to_local":"sales","to_tag":null,"received_at":"\(at)","is_read":\(read),"archived":\(archived),
     "attachment_count":0,"thread_key":"t1","route":"routed"}
    """
}
