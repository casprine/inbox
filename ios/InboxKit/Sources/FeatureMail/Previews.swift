import SwiftUI
import DesignSystem
import Domain

/// Builds a mock-seeded environment for previews.
struct MockHost<Content: View>: View {
    @State private var env: AppEnvironment?
    @ViewBuilder let content: (AppEnvironment) -> Content
    var body: some View {
        Group { if let env { content(env) } else { Color.clear } }
            .task { env = try? await AppEnvironment.mock() }
    }
}

#Preview("App") { MockHost { AppRoot(environment: $0, useMockData: true) } }

#Preview("Business page") {
    MockHost { env in
        NavigationStack { BusinessPageView(env: env, businessID: MockData.acme, live: LiveEvents(), path: .constant([])) }
            .inboxTheme(.system)
    }
}

#Preview("All businesses, dark") {
    MockHost { env in
        NavigationStack { AllBusinessesView(env: env, live: LiveEvents(), path: .constant([])) }
            .inboxTheme(.dark)
    }
}
