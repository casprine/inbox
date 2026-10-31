import SwiftUI
import DesignSystem
import Domain

/// One business: large title + domain, a chip per address, flat newest-first list.
public struct BusinessPageView: View {
    @State private var model: MessageListModel
    let live: LiveEvents
    @Binding var path: [Route]

    public init(env: AppEnvironment, businessID: UUID, address: String? = nil, live: LiveEvents, path: Binding<[Route]>) {
        _model = State(initialValue: MessageListModel(env: env, scope: .business(businessID), selectedAddress: address))
        self.live = live; _path = path
    }

    public var body: some View {
        MessageListScreen(model: model, live: live, path: $path, searchScope: businessID) {
            VStack(alignment: .leading, spacing: 0) {
                PageHeader(title: model.business?.name ?? " ", subtitle: unreadSubtitle(model.business?.domain, model.totalUnread))
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 8) {
                        ForEach(model.chips) { chip in
                            Chip(chip.address == nil ? "All \(chip.count)" : "\(chip.title) \(chip.count)",
                                 selected: model.selectedAddress == chip.address) { model.selectedAddress = chip.address }
                        }
                    }
                    .padding(.horizontal, 20)
                }
                .padding(.bottom, 8)
            }
        }
    }

    private var businessID: UUID? { if case .business(let id) = model.scope { id } else { nil } }
}
