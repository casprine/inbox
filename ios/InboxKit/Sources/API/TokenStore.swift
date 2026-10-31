import Foundation
import Security

public protocol TokenStore: Sendable {
    func token() async -> String?
    func save(_ token: String) async throws
    func clear() async
}

public actor InMemoryTokenStore: TokenStore {
    private var value: String?
    public init(_ token: String? = nil) { value = token }
    public func token() -> String? { value }
    public func save(_ token: String) { value = token }
    public func clear() { value = nil }
}

public struct KeychainError: Error, Sendable, Equatable { public let status: OSStatus }

/// Stores the device bearer token as a generic password item, device-only and available after first unlock.
public struct KeychainTokenStore: TokenStore {
    public let service: String
    public let account: String
    public init(service: String = "com.casprine.inbox", account: String = "device-token") {
        self.service = service; self.account = account
    }

    private var baseQuery: [String: Any] {
        [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: account]
    }

    public func token() async -> String? {
        var q = baseQuery
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess, let data = out as? Data else { return nil }
        return String(data: data, encoding: .utf8)
    }

    public func save(_ token: String) async throws {
        let data = Data(token.utf8)
        let update = SecItemUpdate(baseQuery as CFDictionary, [kSecValueData as String: data] as CFDictionary)
        if update == errSecSuccess { return }
        guard update == errSecItemNotFound else { throw KeychainError(status: update) }
        var add = baseQuery
        add[kSecValueData as String] = data
        add[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let status = SecItemAdd(add as CFDictionary, nil)
        guard status == errSecSuccess else { throw KeychainError(status: status) }
    }

    public func clear() async { SecItemDelete(baseQuery as CFDictionary) }
}
