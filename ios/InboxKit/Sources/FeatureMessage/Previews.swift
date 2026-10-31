import SwiftUI
import DesignSystem
import Domain

struct MessageMockHost<Content: View>: View {
    @State private var env: AppEnvironment?
    @ViewBuilder let content: (AppEnvironment) -> Content
    var body: some View {
        Group { if let env { content(env) } else { Color.clear } }
            .task { env = try? await AppEnvironment.mock() }
    }
}

#Preview("Invoice (HTML)") {
    MessageMockHost { env in NavigationStack { MessageScreen(env: env, id: MockContent.invoiceID) { _ in } }.inboxTheme(.system) }
}
#Preview("Thread") {
    MessageMockHost { env in NavigationStack { MessageScreen(env: env, id: MockContent.threadID) { _ in } }.inboxTheme(.system) }
}
#Preview("Images blocked") {
    MessageMockHost { env in NavigationStack { MessageScreen(env: env, id: MockContent.kettleID) { _ in } }.inboxTheme(.system) }
}
#Preview("Details") {
    MessageMockHost { env in NavigationStack { DetailsScreen(env: env, id: MockContent.invoiceID) }.inboxTheme(.system) }
}
#Preview("Original") {
    MessageMockHost { env in NavigationStack { OriginalScreen(env: env, id: MockContent.invoiceID) }.inboxTheme(.system) }
}
