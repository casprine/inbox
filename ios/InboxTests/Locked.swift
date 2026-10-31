import Foundation

/// Minimal lock (Synchronization.Mutex needs iOS 18; deployment target is 17).
final class Mutex<Value>: @unchecked Sendable {
    private let lock = NSLock()
    private var value: Value
    init(_ v: Value) { value = v }
    func withLock<R>(_ body: (inout Value) throws -> R) rethrows -> R { lock.lock(); defer { lock.unlock() }; return try body(&value) }
}
