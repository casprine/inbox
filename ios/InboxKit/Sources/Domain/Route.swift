import Foundation

/// Typed destinations for the Mail `NavigationStack` (ENGINEERING_PLAN §8.3, §8.6). Other features add their
/// cases here and handle them in the stack's destination mapping.
public enum Route: Hashable, Sendable {
    case allBusinesses
    case business(UUID, address: String?)
    case message(UUID)
    case details(UUID)
    case original(UUID)
    case attention
    case unrouted
    case failed
    case failedDetail(UUID)
    case search(scope: UUID?)
}
