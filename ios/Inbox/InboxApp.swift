import SwiftUI
import DesignSystem
import Domain
import FeatureMail

@main
struct InboxApp: App {
    /// `-UseMockData` (or env `INBOX_USE_MOCK_DATA=1`) runs against seeded sample data with no server.
    private static let useMockData = ProcessInfo.processInfo.arguments.contains("-UseMockData")
        || ProcessInfo.processInfo.environment["INBOX_USE_MOCK_DATA"] == "1"

    /// Live environment (Keychain + on-disk GRDB); falls back to the in-memory preview one if the store can't open.
    @State private var environment: AppEnvironment? = Self.useMockData
        ? try? AppEnvironment.preview()
        : ((try? AppEnvironment.live()) ?? (try? AppEnvironment.preview()))

    var body: some Scene {
        WindowGroup {
            if let environment {
                AppRoot(environment: environment, useMockData: Self.useMockData)
                    .environment(\.appEnvironment, environment)
            } else {
                DesignGallery()
            }
        }
    }
}
