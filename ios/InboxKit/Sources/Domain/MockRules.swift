import Foundation
import API
import Persistence

/// Canned rules / attention endpoints for `-UseMockData` and previews (served by the offline API on `preview.invalid`).
public enum MockRules {
    public static func ruleID(_ n: Int) -> UUID { UUID(uuidString: String(format: "00000000-0000-4000-8000-0000000000C%01d", n))! }
    public static let failedID = UUID(uuidString: "00000000-0000-4000-8000-0000000000D1")!

    public static func records() -> [RuleRecord] {
        [
            RuleRecord(id: ruleID(1), name: "Invoices", position: 0, businessId: MockData.acme, matchToLocal: "invoices", gmailLabel: "Acme/Invoices", messageCount: 14),
            RuleRecord(id: ruleID(2), name: "Receipts", position: 1, matchFrom: "@stripe.com", matchSubject: "receipt", gmailLabel: "Receipts", messageCount: 9),
            RuleRecord(id: ruleID(3), name: "Press", position: 2, businessId: MockData.acme, matchToLocal: "press", gmailLabel: "Acme/Press", gmailArchive: true, labelOnly: false, messageCount: 3),
            RuleRecord(id: ruleID(4), name: "Accounts", position: 3, enabled: false, businessId: MockData.northgate, matchToLocal: "accounts", gmailLabel: "Northgate/Accounts", messageCount: 5),
            RuleRecord(id: ruleID(5), name: "Hosting", position: 4, businessId: MockData.lumen, matchToLocal: "billing", gmailLabel: "Lumen", messageCount: 2),
        ]
    }

    static func ruleObject(_ r: RuleRecord) -> [String: Any] {
        func n(_ s: String?) -> Any { s ?? NSNull() }
        return ["id": r.id.uuidString.lowercased(), "name": r.name, "position": r.position, "enabled": r.enabled,
                "business_id": r.businessId?.uuidString.lowercased() ?? NSNull(), "match_to_local": n(r.matchToLocal),
                "match_from": n(r.matchFrom), "match_subject": n(r.matchSubject), "gmail_label": n(r.gmailLabel),
                "gmail_archive": r.gmailArchive, "gmail_mark_read": r.gmailMarkRead, "label_only": r.labelOnly,
                "imported_from_filter": NSNull(), "message_count": r.messageCount,
                "created_at": "2026-09-01T09:00:00Z", "updated_at": "2026-09-01T09:00:00Z"]
    }

    private static func iso(_ d: Date) -> String { ISO8601DateFormatter().string(from: d) }

    static func unroutedMessages(now: Date = .now) -> [MessageRecord] {
        let snippets = [201: "Hi, attached is our 2027 wholesale price list for the copper range.", 202: "Hello, we'd like to stock your kettles in our studio shop. Could you send terms?",
                        203: "Thanks, the samples arrived this morning and look great."]
        func m(_ n: Int, _ biz: UUID, _ to: String, _ from: String, _ subject: String, ago: TimeInterval) -> MessageRecord {
            MessageRecord(id: MockData.messageID(n), businessId: biz, fromAddr: "\(from.lowercased().replacingOccurrences(of: " ", with: "."))@example.com",
                          fromName: from, subject: subject, snippet: snippets[n] ?? "", toLocal: to, receivedAt: now.addingTimeInterval(-ago),
                          threadKey: "u\(n)", route: "unrouted")
        }
        return [
            m(201, MockData.acme, "hello", "Kettle House", "Wholesale price list 2027", ago: 3_600),
            m(202, MockData.acme, "hello", "Tomás Weber", "Wholesale enquiry for the studio", ago: 20_000),
            m(203, MockData.northgate, "sales", "Priya Raman", "Re: Samples shipped", ago: 90_000),
        ]
    }

    private static func messageObject(_ r: MessageRecord) -> [String: Any] {
        let e = APIClient.makeEncoder(); e.dateEncodingStrategy = .iso8601
        guard let d = try? e.encode(r), let o = try? JSONSerialization.jsonObject(with: d) as? [String: Any] else { return [:] }
        return o
    }

    private static func ref(_ r: MessageRecord) -> [String: Any] {
        ["id": r.id.uuidString.lowercased(), "business_id": r.businessId?.uuidString.lowercased() ?? NSNull(), "to_local": r.toLocal ?? NSNull(),
         "from_addr": r.fromAddr ?? NSNull(), "from_name": r.fromName ?? NSNull(), "subject": r.subject ?? NSNull(), "received_at": iso(r.receivedAt)]
    }

    static func unrouted() -> [String: Any] {
        let all = unroutedMessages()
        let groups: [[String: Any]] = [(MockData.acme, "Acme Studio", "acme-studio.com"), (MockData.northgate, "Northgate Ltd", "northgate.co")].map { id, name, domain in
            let ms = all.filter { $0.businessId == id }
            return ["business": ["id": id.uuidString.lowercased(), "name": name, "domain": domain], "count": ms.count, "messages": ms.map(messageObject)]
        }
        return ["groups": groups, "total": all.count]
    }

    static func failed() -> [String: Any] {
        var m = messageObject(MessageRecord(id: failedID, businessId: MockData.acme, fromAddr: "billing@lumen.host", fromName: "Lumen Hosting",
                                            subject: "Backup archive", toLocal: "billing", receivedAt: .now.addingTimeInterval(-86_400 * 29),
                                            threadKey: "f1", route: "unrouted"))
        m["failure_reason"] = "store_failed"; m["env_to"] = "billing@acme-studio.com"; m["size_bytes"] = 4_400_000; m["has_raw"] = false
        return ["messages": [m]]
    }

    /// Reads the request like the real server would: lists, order, create, patch, delete, test, retry, dismiss.
    static func response(for req: URLRequest) -> (status: Int, headers: [String: String], body: Data)? {
        // The sign-in screen posts to whatever address was typed, so the mock login answers on any host.
        guard let url = req.url, url.host == MockContent.host || url.path == "/v1/auth/login" else { return nil }
        let method = req.httpMethod ?? "GET"
        let path = url.path
        func ok(_ o: Any, _ status: Int = 200) -> (Int, [String: String], Data) {
            (status, ["Content-Type": "application/json"], (try? JSONSerialization.data(withJSONObject: o)) ?? Data())
        }
        if let g = gmailResponse(method: method, path: path, body: bodyData(req), query: url.query) { return g }
        let body = bodyData(req)
        let bodyJSON = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any] ?? [:]
        switch (method, path) {
        case ("GET", "/v1/rules"): return ok(["rules": records().map(ruleObject)])
        case ("PUT", "/v1/rules:order"):
            let ids = ((bodyJSON["ids"] as? [String]) ?? []).map { $0.lowercased() }
            let all = records()
            let ordered = ids.compactMap { id in all.first { $0.id.uuidString.lowercased() == id } } + all.filter { !ids.contains($0.id.uuidString.lowercased()) }
            return ok(["rules": ordered.enumerated().map { i, r in var r = r; r.position = i; return ruleObject(r) }])
        case ("POST", "/v1/rules"):
            return invalid(bodyJSON) ?? ok(created(bodyJSON, id: UUID()), 201)
        case ("POST", "/v1/rules:test"):
            let draft = bodyJSON["rule"] as? [String: Any] ?? [:]
            if let bad = invalid(draft) { return bad }
            return ok(testResult(draft, scope: bodyJSON["scope"] as? String ?? "all"))
        case ("GET", "/v1/attention"):
            return ok(["unrouted": ["count": 3, "by_business": []], "failed": ["count": 1], "total": 4])
        case ("GET", "/v1/attention/unrouted"): return ok(unrouted())
        case ("GET", "/v1/attention/failed"): return ok(failed())
        default: break
        }
        let parts = path.split(separator: "/").map(String.init)
        if parts.count == 3, parts[1] == "rules", let id = UUID(uuidString: parts[2]) {
            if method == "DELETE" { return (204, [:], Data()) }
            if method == "PATCH" {
                if let bad = invalid(bodyJSON) { return bad }
                var r = records().first { $0.id == id } ?? RuleRecord(id: id, name: "Rule", position: 0)
                if let e = bodyJSON["enabled"] as? Bool { r.enabled = e }
                var o = ruleObject(r)
                for (k, v) in bodyJSON where k != "id" { o[k] = v }
                return ok(o)
            }
        }
        if parts.count == 3, parts[1] == "failed", method == "POST" {
            if parts[2].hasSuffix(":retry") { return ok(["status": "queued", "id": String(parts[2].dropLast(6))], 202) }
            if parts[2].hasSuffix(":dismiss") { return (204, [:], Data()) }
        }
        return nil
    }

    /// `-GmailState connected|needs_reauth|catching_up|not_connected` (default connected) picks the mock status.
    static func gmailStatusObject() -> [String: Any] {
        let mode = UserDefaults.standard.string(forKey: "GmailState") ?? "connected"
        let now = iso(.now), earlier = iso(.now.addingTimeInterval(-3_600))
        switch mode {
        case "needs_reauth": return ["state": "needs_reauth", "email": "sam@gmail.com", "connected_at": earlier, "last_write_at": earlier, "waiting": 14, "rate_limited_until": NSNull(), "configured": true]
        case "catching_up": return ["state": "connected", "email": "sam@gmail.com", "connected_at": earlier, "last_write_at": now, "waiting": 5, "rate_limited_until": NSNull(), "configured": true]
        case "rate_limited": return ["state": "connected", "email": "sam@gmail.com", "connected_at": earlier, "last_write_at": earlier, "waiting": 3, "rate_limited_until": iso(.now.addingTimeInterval(600)), "configured": true]
        case "not_connected": return ["state": "not_connected", "email": NSNull(), "connected_at": NSNull(), "last_write_at": NSNull(), "waiting": 0, "rate_limited_until": NSNull(), "configured": true]
        default: return ["state": "connected", "email": "sam@gmail.com", "connected_at": earlier, "last_write_at": now, "waiting": 0, "rate_limited_until": NSNull(), "configured": true]
        }
    }

    static var filters: [[String: Any]] {
        func f(_ id: String, from: String? = nil, to: String? = nil, subject: String? = nil, label: String?, name: String, ok: Bool = true, reason: String? = nil) -> [String: Any] {
            ["id": id, "from": from ?? NSNull(), "to": to ?? NSNull(), "subject": subject ?? NSNull(), "label": label ?? NSNull(), "archive": false,
             "mark_read": false, "delete": !ok && reason == "deletes_mail", "forward": NSNull(), "importable": ok, "reason": reason ?? NSNull(),
             "already_imported": false, "rule": ok ? ["name": name, "gmail_label": label ?? NSNull()] as [String: Any] : NSNull()]
        }
        return [f("f1", from: "stripe.com", label: "Kettle/Orders", name: "Orders"), f("f2", to: "invoices@acme-studio.com", label: "Acme/Invoices", name: "Invoices"),
                f("f3", from: "github.com", label: "Alerts", name: "Alerts"), f("f4", subject: "newsletter", label: "Reading", name: "Reading"),
                f("f5", from: "linkedin.com", label: nil, name: "LinkedIn", ok: false, reason: "deletes_mail")]
    }

    /// `-BackfillState none|running|paused|done` (default none).
    static func backfillObjects(id: UUID?) -> [[String: Any]] {
        let mode = UserDefaults.standard.string(forKey: "BackfillState") ?? "none"
        guard mode != "none" else { return [] }
        func b(_ biz: UUID, _ name: String, _ domain: String, est: Int, scanned: Int, sorted: Int, unrouted: Int) -> [String: Any] {
            let status = mode == "done" ? "done" : mode == "paused" ? "paused" : "running"
            return ["id": (id ?? UUID(uuidString: "00000000-0000-4000-8000-00000000B\(String(format: "%03d", abs(domain.hashValue) % 1000))")!).uuidString.lowercased(),
                    "business_id": biz.uuidString.lowercased(), "business_name": name, "domain": domain, "since": "2026-07-13", "status": status,
                    "estimated": est, "scanned": mode == "done" ? est : scanned, "imported": mode == "done" ? est - 12 : scanned - 8, "sorted": sorted, "unrouted": unrouted, "skipped": 8,
                    "paused_until": mode == "paused" ? iso(.now.addingTimeInterval(600)) : NSNull(), "pause_reason": mode == "paused" ? "rate_limited" : NSNull(),
                    "last_error": NSNull(), "created_at": iso(.now.addingTimeInterval(-3_600)), "started_at": iso(.now.addingTimeInterval(-3_500)),
                    "finished_at": mode == "done" ? iso(.now.addingTimeInterval(-600)) : NSNull()]
        }
        return [b(MockData.acme, "Acme Studio", "acme-studio.com", est: 1_600, scanned: 760, sorted: 700, unrouted: 52),
                b(MockData.northgate, "Northgate Ltd", "northgate.co", est: 900, scanned: 400, sorted: 360, unrouted: 32),
                b(MockData.lumen, "Lumen Hosting", "lumen.host", est: 680, scanned: 80, sorted: 70, unrouted: 6)]
    }

    /// `-HealthState ok|needs_you` (default needs_you).
    static func healthObject() -> [String: Any] {
        let bad = (UserDefaults.standard.string(forKey: "HealthState") ?? "needs_you") != "ok"
        func c(_ id: String, _ title: String, _ state: String, _ msg: String, fix: String? = nil, biz: UUID? = nil) -> [String: Any] {
            ["id": id, "business_id": biz?.uuidString.lowercased() ?? NSNull(), "title": title, "state": state, "message": msg, "fix_route": fix ?? NSNull()]
        }
        var checks = [c("mail_arriving", "Mail arriving · Acme Studio", "ok", "Mail is arriving", biz: MockData.acme),
                      c("mail_arriving", "Mail arriving · Northgate Ltd", "ok", "Mail is arriving", biz: MockData.northgate),
                      c("worker", "Worker", "ok", "Running", fix: nil), c("alfather", "Alfather", "ok", "Connected", fix: "alfather")]
        if bad {
            checks.insert(c("mail_arriving", "Mail arriving · Lumen Hosting", "warn", "lumen.host quiet for 6 days", fix: "business/\(MockData.lumen.uuidString.lowercased())/routing", biz: MockData.lumen), at: 2)
            checks.append(c("gmail", "Gmail", "error", "Signed out · 14 waiting", fix: "gmail"))
        } else { checks.append(c("gmail", "Gmail", "ok", "Connected")) }
        checks.append(c("queue", "Queue", bad ? "warn" : "ok", bad ? "14 waiting" : "Nothing waiting"))
        return ["state": bad ? "needs_you" : "ok", "checked_at": iso(.now.addingTimeInterval(-60)), "checks": checks]
    }
    /// `-AlfatherState not_paired|connected|reconnecting|unreachable` (default connected).
    static func alfatherObject() -> [String: Any] {
        let mode = UserDefaults.standard.string(forKey: "AlfatherState") ?? "connected"
        let paired = mode != "not_paired"
        let seen: Date = mode == "unreachable" ? .now.addingTimeInterval(-86_400) : mode == "reconnecting" ? .now.addingTimeInterval(-420) : .now
        return ["paired": paired, "connected": mode == "connected", "paired_at": paired ? iso(.now.addingTimeInterval(-86_400 * 3)) : NSNull(),
                "last_seen_at": paired ? iso(seen) : NSNull(), "waiting": mode == "unreachable" ? 41 : 0, "cursor_seq": 120, "mcp_url": paired ? "https://inbox.example.com/mcp" : NSNull()]
    }

    static var cloudflareDomains: [[String: Any]] {
        func d(_ id: String, _ domain: String, ok: Bool) -> [String: Any] {
            ["zone_id": id, "domain": domain, "status": "active", "routing": ok ? "ok" : "to_email", "already_added": false, "selectable": ok]
        }
        return [d("z1", "acme-studio.com", ok: true), d("z2", "fieldnotes.co", ok: true), d("z3", "kettle.house", ok: false),
                d("z4", "tallyho.app", ok: true), d("z5", "casprine.dev", ok: true)]
    }

    private static func gmailResponse(method: String, path: String, body: Data, query: String? = nil) -> (status: Int, headers: [String: String], body: Data)? {
        func ok(_ o: Any, _ status: Int = 200) -> (Int, [String: String], Data) {
            (status, ["Content-Type": "application/json"], (try? JSONSerialization.data(withJSONObject: o)) ?? Data())
        }
        if method == "POST", path.hasPrefix("/v1/cloudflare/domains/"), path.hasSuffix(":check") {
            let zone = String(path.dropFirst("/v1/cloudflare/domains/".count).dropLast(":check".count))
            let d = cloudflareDomains.first { $0["zone_id"] as? String == zone } ?? cloudflareDomains[0]
            var fixed = d; fixed["routing"] = "ok"; fixed["selectable"] = true
            return ok(fixed)
        }
        if method == "POST", path.hasPrefix("/v1/businesses/"), path.hasSuffix("/routing:check") {
            let id = String(path.dropFirst("/v1/businesses/".count).dropLast("/routing:check".count))
            return ok(["business_id": id, "routing": "ok", "checked_at": iso(.now)])
        }
        if method == "POST", path.hasPrefix("/v1/backfills/") {
            let id = String(path.dropFirst("/v1/backfills/".count).split(separator: ":").first ?? "")
            return ok(backfillObjects(id: UUID(uuidString: id)).first ?? [:])
        }
        switch (method, path) {
        case ("GET", "/v1/prefs"): return ok(["retention_days": 365, "image_allow_senders": [], "preview_lines": 2])
        case ("PUT", "/v1/prefs"): return ok(["retention_days": 365, "image_allow_senders": [], "preview_lines": 2])
        case ("POST", "/v1/data:confirm"): return ok(["token": "DELETE-4F7K2M9Q", "expires_at": iso(.now.addingTimeInterval(300)), "expires_in": 300])
        case ("DELETE", "/v1/data"): return ok(["deleted_messages": 120, "deleted_blobs": 240])
        case ("GET", "/v1/backfills:estimate"):
            let days = URLComponents(string: "x://x?\(query ?? "")")?.queryItems?.first { $0.name == "days" }?.value.flatMap(Int.init) ?? 90
            let total = days <= 30 ? 1_100 : days <= 90 ? 3_200 : 12_800
            return ok(["since": "2026-07-13", "total": total, "eta_seconds": total / 10,
                       "businesses": [["business_id": MockData.acme.uuidString.lowercased(), "name": "Acme Studio", "domain": "acme-studio.com", "estimate": total / 2]]])
        case ("GET", "/v1/backfills"): return ok(["backfills": backfillObjects(id: nil)])
        case ("POST", "/v1/backfills"): return ok(["backfills": backfillObjects(id: nil)], 201)
        case ("GET", "/v1/health"): return ok(healthObject())
        case ("GET", "/v1/alfather/status"): return ok(alfatherObject())
        case ("POST", "/v1/alfather/pairings"): return ok(["consumer_id": "c1", "mcp_url": "https://inbox.example.com/mcp", "token": "alf_mock", "scope": "mail.read", "include_existing": false], 201)
        case ("DELETE", "/v1/alfather"): return (204, [:], Data())
        case ("POST", "/v1/auth/login"):
            // Mock passwords drive the sign-in states: "wrong-password" 401, "locked-out" 429 (4:32), "server-down" 503; anything else signs in.
            let pw = ((try? JSONSerialization.jsonObject(with: body)) as? [String: Any])?["password"] as? String ?? ""
            func problem(_ status: Int, _ code: String, _ headers: [String: String] = [:]) -> (Int, [String: String], Data) {
                let o: [String: Any] = ["type": "about:blank", "title": code, "status": status, "code": code]
                return (status, ["Content-Type": "application/problem+json"].merging(headers) { $1 }, (try? JSONSerialization.data(withJSONObject: o)) ?? Data())
            }
            switch pw {
            case "wrong-password": return problem(401, "invalid_credentials")
            case "locked-out": return problem(429, "rate_limited", ["Retry-After": "272"])
            case "server-down": return problem(503, "internal")
            default: return ok(["device_id": UUID().uuidString.lowercased(), "token": "mock-device-token"])
            }
        case ("GET", "/v1/cloudflare/domains"): return ok(["domains": cloudflareDomains])
        case ("POST", "/v1/businesses"):
            let b = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any] ?? [:]
            return ok(["id": UUID().uuidString.lowercased(), "name": b["name"] ?? "Business", "domain": b["domain"] ?? "", "sort_order": 0, "hidden": false, "enabled": true,
                       "routing": "ok", "unread": 0, "last_message_at": NSNull(), "addresses": []], 201)
        case ("GET", "/v1/gmail/status"): return ok(gmailStatusObject())
        case ("POST", "/v1/gmail/connect"): return ok(["auth_url": "https://accounts.google.com/o/oauth2/v2/auth?mock=1", "callback_scheme": "inbox", "expires_in": 600])
        case ("POST", "/v1/gmail:disconnect"): return (204, [:], Data())
        case ("GET", "/v1/gmail/labels"):
            return ok(["labels": ["Acme/Invoices", "Acme/Press", "Kettle/Wholesale", "Kettle/Orders", "Northgate/Accounts", "Receipts", "Reading"].map { ["id": $0, "name": $0] }])
        case ("POST", "/v1/gmail/labels"):
            let n = ((try? JSONSerialization.jsonObject(with: body)) as? [String: Any])?["name"] as? String ?? "Label"
            return ok(["id": n, "name": n, "created": true], 201)
        case ("GET", "/v1/gmail/filters"): return ok(["importable": 4, "filters": filters])
        case ("POST", "/v1/rules:import"):
            let ids = ((try? JSONSerialization.jsonObject(with: body)) as? [String: Any])?["filter_ids"] as? [String] ?? []
            let imported = ids.map { ["filter_id": $0, "rule_id": UUID().uuidString.lowercased()] }
            return ok(["imported": imported, "skipped": []])
        default: return nil
        }
    }

    private static func bodyData(_ r: URLRequest) -> Data {
        if let b = r.httpBody { return b }
        guard let s = r.httpBodyStream else { return Data() }
        s.open(); defer { s.close() }
        var data = Data(), buf = [UInt8](repeating: 0, count: 4096)
        while s.hasBytesAvailable { let n = s.read(&buf, maxLength: buf.count); if n <= 0 { break }; data.append(buf, count: n) }
        return data
    }

    private static func created(_ b: [String: Any], id: UUID) -> [String: Any] {
        var o = ruleObject(RuleRecord(id: id, name: b["name"] as? String ?? "Rule", position: 0))
        for (k, v) in b where k != "id" { o[k] = v }
        return o
    }

    /// 422 like the server for an uncompilable subject pattern; nil when fine.
    private static func invalid(_ b: [String: Any]) -> (status: Int, headers: [String: String], body: Data)? {
        guard let s = b["match_subject"] as? String, !s.isEmpty, (try? NSRegularExpression(pattern: s)) == nil else { return nil }
        let col = (s.firstIndex(of: "(").map { s.distance(from: s.startIndex, to: $0) + 1 }) ?? 1
        let msg = "Subject pattern isn't valid: missing closing )."
        let o: [String: Any] = ["type": "about:blank", "title": "Unprocessable Entity", "status": 422, "code": "rule_pattern_invalid", "detail": msg,
                                "field": "match_subject", "column": col, "errors": [["field": "match_subject", "message": msg, "column": col]]]
        return (422, ["Content-Type": "application/problem+json"], (try? JSONSerialization.data(withJSONObject: o)) ?? Data())
    }

    private static func testResult(_ draft: [String: Any], scope: String) -> [String: Any] {
        let re = (draft["match_subject"] as? String).flatMap { $0.isEmpty ? nil : try? NSRegularExpression(pattern: $0, options: .caseInsensitive) }
        func hits(_ ms: [MessageRecord]) -> [MessageRecord] {
            guard let re else { return ms }
            return ms.filter { m in let s = m.subject ?? ""; return re.firstMatch(in: s, range: NSRange(s.startIndex..., in: s)) != nil }
        }
        if scope == "unrouted" {
            let all = unroutedMessages()
            return ["scope": "unrouted", "scanned": all.count, "would_match": hits(all).map(ref), "caught_earlier": []]
        }
        let recent = MockData.messages()
        let match = Array(hits(recent).prefix(2)), early = Array(hits(recent).dropFirst(2).prefix(1))
        return ["scope": "all", "scanned": 200, "would_match": match.map(ref),
                "caught_earlier": early.map { ["message": ref($0), "by_rule": ["id": ruleID(2).uuidString.lowercased(), "name": "Receipts"]] as [String: Any] }]
    }
}
