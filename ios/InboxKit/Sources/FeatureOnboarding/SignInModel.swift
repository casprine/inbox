import Foundation
import Observation
import UIKit
import API
import Domain

/// Which entry opened the sign-in screen (D4 §1.4): "Get started", "I already set this up", or a lost session (401 later).
public enum SignInMode: Sendable, Equatable { case new, existing, signedOut }

/// `passwordShown` is the plain-text twin of `password` (same row); only the screen uses it.
public enum SignInField: Hashable, Sendable { case address, password, passwordShown }

/// Texts of the sign-in screen that depend on the mode or on the lock-out countdown (D4 §1.4, §1.5, §1.8).
public enum SignInCopy {
    public static func title(_ mode: SignInMode) -> String {
        switch mode { case .new: "Connect to your server"; case .existing: "Welcome back"; case .signedOut: "Sign in again" }
    }
    public static func caption(_ mode: SignInMode) -> String {
        switch mode {
        case .new: "Enter the address and password you chose when you set up your Inbox server. You only do this once on this phone."
        case .existing: "Enter your server address and password to pick up where you left off."
        case .signedOut: "You were signed out of this server. Mail already on this phone stays readable."
        }
    }

    /// `4:32`. Under ten minutes the countdown runs per second.
    public static func clock(_ seconds: Int) -> String { String(format: "%d:%02d", seconds / 60, seconds % 60) }

    /// "4:32", "about 14 minutes", "about 1 hour".
    static func wait(_ remaining: Int) -> String {
        if remaining < 600 { return clock(max(remaining, 0)) }
        if remaining < 3_600 { return "about \((remaining + 59) / 60) minutes" }
        let hours = (remaining + 3_599) / 3_600
        return hours == 1 ? "about 1 hour" : "about \(hours) hours"
    }
    public static func lockMessage(remaining: Int) -> String { "Sign-in is paused to protect your server. Try again in \(wait(remaining))." }
    public static func lockButton(remaining: Int) -> String { remaining < 600 ? "Try again in \(clock(remaining))" : "Try again later" }

    /// For VoiceOver: "4 minutes 32 seconds".
    public static func spoken(_ remaining: Int) -> String {
        if remaining >= 600 { return wait(remaining) }
        let m = remaining / 60, s = remaining % 60
        func unit(_ n: Int, _ w: String) -> String { "\(n) \(w)\(n == 1 ? "" : "s")" }
        if m == 0 { return unit(s, "second") }
        return s == 0 ? unit(m, "minute") : "\(unit(m, "minute")) \(unit(s, "second"))"
    }
}

@MainActor @Observable
public final class SignInModel {
    public enum Unreachable: Equatable, Sendable { case network, tls(host: String), server, couldNotSave }
    public enum Issue: Equatable, Sendable {
        /// Inline under the group, address row. `insecure`: plain http:// to a public host.
        case invalidURL(insecure: Bool)
        /// Inline under the group, password row.
        case wrongPassword
        /// Inline under the group, address row.
        case notInboxServer
        /// Banner with "Try again".
        case unreachable(Unreachable)

        public var field: SignInField? {
            switch self { case .invalidURL, .notInboxServer: .address; case .wrongPassword: .password; case .unreachable: nil }
        }
        /// Inline sentence (nil for banners).
        public var inlineText: String? {
            switch self {
            case .invalidURL(let insecure): insecure ? "Use https:// for a server on the internet." : "That doesn't look like a server address. Try something like inbox.example.com."
            case .wrongPassword: "That password isn't right."
            case .notInboxServer: "That address doesn't look like an Inbox server."
            case .unreachable: nil
            }
        }
    }
    public struct FocusRequest: Equatable, Sendable { public let field: SignInField; let serial: Int }

    public nonisolated static let addressKey = "api_base_url"
    /// Lock-outs by server, kept for the launch only so leaving and coming back to the screen still shows the countdown.
    private static var lockouts: [String: Date] = [:]
    public static func forgetLockouts() { lockouts = [:] }

    public let mode: SignInMode
    public var address: String { didSet { if address != oldValue, issue?.field == .address { issue = nil } } }
    public var password = "" { didSet { if password != oldValue, issue?.field == .password { issue = nil } } }
    /// Show/hide. Back to hidden whenever the screen disappears or the app leaves the foreground.
    public var revealed = false
    public private(set) var isLoading = false
    public private(set) var issue: Issue?
    /// `setup_step` from the last successful login (nil when the server doesn't send it).
    public private(set) var setupStep: String?
    public private(set) var focusRequest: FocusRequest?

    private let env: AppEnvironment
    private let defaults: UserDefaults
    private let deviceName: String
    private let now: () -> Date
    private var serial = 0

    public init(env: AppEnvironment, mode: SignInMode, defaults: UserDefaults = .standard, deviceName: String? = nil, now: @escaping () -> Date = { .now }) {
        self.env = env; self.mode = mode; self.defaults = defaults; self.now = now
        self.deviceName = String((deviceName ?? UIDevice.current.name).prefix(100))
        switch mode {
        case .new: address = ""
        case .existing: address = defaults.string(forKey: Self.addressKey) ?? ""
        case .signedOut: address = env.api.baseURL.absoluteString
        }
    }

    /// Server address when the person has filled it in, password otherwise.
    public var initialFocus: SignInField { address.trimmingCharacters(in: .whitespaces).isEmpty ? .address : .password }

    public var canSubmit: Bool {
        !address.trimmingCharacters(in: .whitespaces).isEmpty && !password.isEmpty && !isLoading && lockRemaining() == nil
    }

    // MARK: Lock-out

    private var lockKey: String? { if case .success(let u) = ServerAddress.normalise(address) { u.absoluteString } else { nil } }

    /// Whole seconds left of a 429 lock for the address in the field, or nil when not locked.
    public func lockRemaining(at date: Date? = nil) -> Int? {
        guard let k = lockKey, let until = Self.lockouts[k] else { return nil }
        let left = Int(until.timeIntervalSince(date ?? now()).rounded(.up))
        return left > 0 ? left : nil
    }
    public var lockUntil: Date? { lockKey.flatMap { Self.lockouts[$0] } }

    // MARK: Submit

    private func request(_ field: SignInField) { serial += 1; focusRequest = FocusRequest(field: field, serial: serial) }

    /// Validates, signs in and stores the device token. Returns true on success and then stays "loading" until the caller moves on
    /// (no flash of the form); every failure ends loading and sets exactly one `issue` (or the lock-out banner).
    public func submit() async -> Bool {
        guard canSubmit else { return false }
        issue = nil
        let url: URL
        switch ServerAddress.normalise(address) {
        case .failure(let why):
            issue = .invalidURL(insecure: why == .insecure); request(.address); announce(issue)
            return false
        case .success(let u): url = u
        }
        address = url.absoluteString
        isLoading = true
        do {
            let ok = try await env.api.login(server: url, password: password, deviceName: deviceName)
            setupStep = ok.setupStep
            password = ""
            defaults.set(url.absoluteString, forKey: Self.addressKey)
            Self.lockouts[url.absoluteString] = nil
            return true
        } catch is CancellationError {
            isLoading = false
            return false
        } catch {
            isLoading = false
            switch error as? LoginFailure {
            case .invalidCredentials?:
                password = ""; revealed = false
                issue = .wrongPassword; request(.password)
            case .rateLimited(let after)?:
                lock(url, for: after)
                UIAccessibility.post(notification: .announcement, argument: SignInCopy.lockMessage(remaining: Int(after)))
            case .tls(let host)?: issue = .unreachable(.tls(host: host))
            case .server?: issue = .unreachable(.server)
            case .notInboxServer?: issue = .notInboxServer; request(.address)
            case .unreachable?: issue = .unreachable(.network)
            case nil: issue = .unreachable(.couldNotSave)    // the server accepted the password but the Keychain write failed
            }
            announce(issue)
            return false
        }
    }

    func lock(_ url: URL, for seconds: TimeInterval) { Self.lockouts[url.absoluteString] = now().addingTimeInterval(seconds) }
    /// Snapshot and preview aid: show the state a failed attempt would leave.
    func show(_ issue: Issue?, loading: Bool = false) { self.issue = issue; isLoading = loading }

    /// Back during loading: the owning task is cancelled, this just returns the form to idle.
    public func cancelled() { isLoading = false }

    private func announce(_ issue: Issue?) {
        guard let issue else { return }
        let text: String
        switch issue {
        case .unreachable(let k): text = Self.bannerText(k).title + " " + Self.bannerText(k).message
        default: text = issue.inlineText ?? ""
        }
        UIAccessibility.post(notification: .announcement, argument: text)
    }

    public static func bannerText(_ k: Unreachable) -> (title: String, message: String) {
        switch k {
        case .network: ("Can't reach your server.", "Check the address and your connection, then try again.")
        case .tls(let host): ("Can't reach your server.", "Couldn't make a secure connection to \(host).")
        case .server: ("Can't reach your server.", "Your server answered with an error. Try again in a moment.")
        case .couldNotSave: ("Couldn't finish signing in.", "The password was right, but this phone couldn't save the sign-in. Try again.")
        }
    }
}
