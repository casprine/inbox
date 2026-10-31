import Foundation

/// Debug-only development settings: the dev server's URL and fixed token, from the environment (simulator, Xcode scheme) or, on a
/// phone, from Info.plist values that `Local.xcconfig` fills in (`make ios-local-config`). Only Local.xcconfig fills the plist keys and it is included by Debug and Demo, never Release, so store builds return nil.
public enum DevConfig {
    private static func plist(_ key: String) -> String? {
        (Bundle.main.object(forInfoDictionaryKey: key) as? String).flatMap { $0.isEmpty || $0.hasPrefix("$(") ? nil : $0 }
    }
    private static func env(_ key: String) -> String? { ProcessInfo.processInfo.environment[key].flatMap { $0.isEmpty ? nil : $0 } }

    public static var token: String? {
        // `-NoDevToken 1` (live sign-in tests): behave like a build without a seeded token even when Local.xcconfig has one.
        if UserDefaults.standard.bool(forKey: "NoDevToken") { return nil }
        return env("INBOX_DEV_TOKEN") ?? plist("InboxDevToken")
    }

    public static var baseURL: String? {
        env("INBOX_API_BASE_URL") ?? plist("InboxDevAPIBaseURL")
    }
}
