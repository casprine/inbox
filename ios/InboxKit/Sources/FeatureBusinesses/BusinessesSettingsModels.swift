import Foundation
import Observation
import API
import Domain
import Persistence

/// Settings → Businesses (BZ01/BZ02): every business, reorder, hide.
@MainActor @Observable
public final class BusinessesSettingsModel {
    public private(set) var businesses: [BusinessRecord] = []
    public private(set) var hasLoaded = false
    public private(set) var notice: String?
    private let env: AppEnvironment
    public init(env: AppEnvironment) { self.env = env }

    /// Test hook: shows a fixed list without observing the database.
    public func observe_once_for_tests(_ list: [BusinessRecord]) { businesses = list; hasLoaded = true }

    public func observe() async {
        businesses = (try? await env.businesses.all()) ?? []
        if !businesses.isEmpty { hasLoaded = true }
        for await list in env.businesses.observe() { businesses = list; hasLoaded = true }
    }
    public func refresh() async {
        do { try await env.businesses.refresh(); notice = nil } catch { notice = "Couldn't refresh. Showing what's saved on this phone." }
        hasLoaded = true
    }
    public func setHidden(_ hidden: Bool, _ b: BusinessRecord) async {
        do { try await env.businesses.setHidden(hidden, id: b.id); notice = nil } catch { notice = "Couldn't change “\(b.name)”." }
    }
    public func move(from: IndexSet, to: Int) async {
        var ids = businesses.map(\.id)
        let moving = from.sorted().map { ids[$0] }
        for i in from.sorted().reversed() { ids.remove(at: i) }
        ids.insert(contentsOf: moving, at: min(max(to - from.filter { $0 < to }.count, 0), ids.count))
        do { try await env.businesses.reorder(ids: ids); notice = nil } catch { notice = "Couldn't save the new order. It's back to how it was." }
    }
    public func canMove(_ b: BusinessRecord, by delta: Int) -> Bool {
        guard let i = businesses.firstIndex(where: { $0.id == b.id }) else { return false }
        return businesses.indices.contains(i + delta)
    }
    /// VoiceOver "Move up/down": one step in the list.
    public func move(_ b: BusinessRecord, by delta: Int) async {
        guard let i = businesses.firstIndex(where: { $0.id == b.id }), canMove(b, by: delta) else { return }
        await move(from: IndexSet(integer: i), to: delta > 0 ? i + 2 : i - 1)
    }

    /// "Catch-all → To Inbox" etc. for a routing value.
    public static func routingText(_ r: String) -> (value: String, ok: Bool) {
        switch r {
        case "ok": ("To Inbox", true)
        case "to_email": ("To email", false)
        case "none": ("Not set up", false)
        default: ("Unknown", false)
        }
    }
}

/// BZ03 Business detail, BZ04 remove.
@MainActor @Observable
public final class BusinessDetailModel {
    public private(set) var business: BusinessRecord?
    public var name = ""
    public private(set) var notice: String?
    public var confirmingRemove = false
    public private(set) var removing = false
    public let id: UUID
    private let env: AppEnvironment
    public init(env: AppEnvironment, id: UUID) { self.env = env; self.id = id }

    public func observe() async {
        for await list in env.businesses.observe() {
            business = list.first { $0.id == id }
            if let b = business, name.isEmpty { name = b.name }
        }
    }
    public func load() async {
        business = ((try? await env.businesses.all()) ?? []).first { $0.id == id }
        if let b = business { name = b.name }
    }
    public func commitName() async {
        let n = name.trimmingCharacters(in: .whitespaces)
        guard let b = business, !n.isEmpty, n != b.name else { name = business?.name ?? name; return }
        do { try await env.businesses.rename(id: id, to: n); notice = nil } catch { notice = "Couldn't rename."; name = b.name }
    }
    public func setHidden(_ h: Bool) async {
        do { try await env.businesses.setHidden(h, id: id); notice = nil } catch { notice = "Couldn't change visibility." }
    }
    /// Deletes stored copies and the business; Gmail is untouched. Returns true when gone.
    public func remove() async -> Bool {
        removing = true; defer { removing = false }
        do { try await env.businesses.remove(id: id); return true } catch { notice = "Couldn't remove. Nothing was changed."; return false }
    }
    public var addresses: [API.Address] { business?.addresses ?? [] }
    /// "90 days imported" once an import for this business finished; otherwise "Not imported".
    public var importSummary: String {
        let b = env.backfill.backfills.first { $0.businessId == id && $0.status == .done }
        guard let b, let days = env.backfill.days ?? env.backfill.rangeDays else { return b == nil ? "Not imported" : "Imported" }
        return days == 365 ? "1 year imported" : "\(days) days imported"
    }
}

/// BZ05 Add a business: pick a domain from Cloudflare.
@MainActor @Observable
public final class AddBusinessModel {
    public private(set) var domains: [CloudflareDomain] = []
    public var selected: String?
    public private(set) var isLoading = true
    public private(set) var error: String?
    public private(set) var isSaving = false
    private let env: AppEnvironment
    public init(env: AppEnvironment) { self.env = env }

    public var chosen: CloudflareDomain? { domains.first { $0.id == selected } }

    public func load() async {
        do {
            domains = try await env.api.cloudflareDomains().filter { !$0.alreadyAdded }
            selected = selected ?? domains.first(where: \.selectable)?.id
            error = nil
        } catch let e as APIError {
            if case .problem(let p, _) = e, p.code == "cloudflare_not_configured" { error = "Cloudflare isn't connected on your server yet." }
            else { error = "Couldn't load your Cloudflare domains." }
        } catch { self.error = "Couldn't load your Cloudflare domains." }
        isLoading = false
    }
    public func select(_ d: CloudflareDomain) { if d.selectable { selected = d.id } }
    public func add() async -> Bool {
        guard let d = chosen, d.selectable, !isSaving else { return false }
        isSaving = true; defer { isSaving = false }
        do { try await env.businesses.add(name: nameFor(d.domain), domain: d.domain, zoneId: d.zoneId); return true }
        catch { self.error = "Couldn't add \(d.domain). Try again."; return false }
    }
    private func nameFor(_ domain: String) -> String {
        let label = domain.split(separator: ".").first.map(String.init) ?? domain
        return label.split(whereSeparator: { $0 == "-" || $0 == "_" }).map { $0.prefix(1).uppercased() + $0.dropFirst() }.joined(separator: " ")
    }
}

/// BZ06 Routing problem.
@MainActor @Observable
public final class RoutingProblemModel {
    public private(set) var business: BusinessRecord?
    public private(set) var checking = false
    public private(set) var result: CloudflareDomain.Routing?
    public private(set) var error: String?
    public let id: UUID
    private let env: AppEnvironment
    public init(env: AppEnvironment, id: UUID) { self.env = env; self.id = id }

    public func load() async { business = ((try? await env.businesses.all()) ?? []).first { $0.id == id }; result = business.flatMap { CloudflareDomain.Routing(rawValue: $0.routing) } }
    public var isFixed: Bool { result == .ok }
    public func checkAgain() async {
        checking = true; defer { checking = false }
        do { result = try await env.api.checkRouting(businessId: id).routing; error = nil; try? await env.businesses.refresh(); await loadBusiness() }
        catch let e as APIError {
            if case .problem(let p, _) = e, p.code == "cloudflare_zone_unknown" { error = "This domain isn't in your Cloudflare account any more." }
            else { error = "Couldn't check right now. Try again." }
        } catch { self.error = "Couldn't check right now. Try again." }
    }
    private func loadBusiness() async { business = ((try? await env.businesses.all()) ?? []).first { $0.id == id } }
    /// Days since the last message, for "No mail for 6 days."
    public var quietDays: Int? {
        guard let d = business?.lastMessageAt else { return nil }
        let n = Calendar.current.dateComponents([.day], from: d, to: AppClock.now).day ?? 0
        return n >= 1 ? n : nil
    }
}
