import SwiftUI

public struct DesignGallery: View {
    @State private var mode: ThemeMode
    @State private var isOn = true
    @State private var tab: InboxTab = .mail
    @State private var chip = 0
    public init(initialMode: ThemeMode = .system) { _mode = State(initialValue: initialMode) }

    public var body: some View { Inner(mode: $mode, isOn: $isOn, tab: $tab, chip: $chip).inboxTheme(mode) }

    private struct Inner: View {
        @Environment(\.palette) private var p
        @Binding var mode: ThemeMode
        @Binding var isOn: Bool
        @Binding var tab: InboxTab
        @Binding var chip: Int
        var body: some View {
            VStack(spacing: 0) {
                ScrollView {
                    VStack(alignment: .leading, spacing: 24) {
                        Text("Design gallery").font(.inboxHeading(32, relativeTo: .largeTitle)).foregroundStyle(p.text)
                        Picker("Theme", selection: $mode) {
                            ForEach(ThemeMode.allCases) { Text($0.rawValue.capitalized).tag($0) }
                        }.pickerStyle(.segmented)

                        ScrollView(.horizontal, showsIndicators: false) {
                            HStack { ForEach(Array(["All", "Acme", "Bluebird", "Fern"].enumerated()), id: \.offset) { i, t in
                                Chip(t, selected: chip == i) { chip = i } } }
                        }
                        InsetGroup("Rows") {
                            MailRow(.init(id: "1", sender: "Acme Billing", subject: "Invoice 1042", preview: "Your invoice for October is ready to view.", time: "9:41", isUnread: true))
                            MailRow(.init(id: "2", sender: "Bluebird", subject: "Weekly digest", preview: "Seven things you missed this week.", time: "Mon"))
                            MailRow(.init(id: "3", sender: "Fern", subject: "Selected row", preview: "Selection mode shows a check.", time: "Sun"), mode: .selecting(selected: true))
                            MailRow(nil)
                        }
                        InsetGroup("Settings") {
                            InsetRow("Load remote images", subtitle: "Per sender") { SquareSwitch(isOn: $isOn) }
                            InsetRow("Gmail", showsDivider: false) { HStack { StatusDot(.ok); Text("Connected").font(.inboxBody(15)).foregroundStyle(p.secondary) } }
                        }
                        Banner(title: "Gmail signed out", message: "Reconnect to keep archives in sync.", actionTitle: "Fix")
                        InboxProgressBar(value: 0.4)
                        Toast("Archived")
                        PrimaryButton("Continue")
                        PrimaryButton("Add business", style: .secondary)
                        PrimaryButton("Delete rule", style: .destructive)
                        PrimaryButton("Skip for now", style: .plain)
                        SheetHeader("Choose label")
                        EmptyState(title: "Nothing here", message: "New mail will appear as it arrives.", actionTitle: "Refresh")
                    }
                    .padding(16)
                }
                InboxTabBar(selection: $tab)
            }
            .background(p.ground.ignoresSafeArea())
        }
    }
}
