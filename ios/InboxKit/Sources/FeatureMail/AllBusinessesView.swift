import SwiftUI
import DesignSystem
import Domain

/// Every business's mail in one list, grouped under small business headers.
public struct AllBusinessesView: View {
    @State private var model: MessageListModel
    let live: LiveEvents
    @Binding var path: [Route]

    public init(env: AppEnvironment, live: LiveEvents, path: Binding<[Route]>) {
        _model = State(initialValue: MessageListModel(env: env, scope: .all))
        self.live = live; _path = path
    }

    public var body: some View {
        MessageListScreen(model: model, live: live, path: $path, searchScope: nil) {
            PageHeader(title: "All businesses", subtitle: unreadSubtitle(nil, model.totalUnread))
        }
    }
}
