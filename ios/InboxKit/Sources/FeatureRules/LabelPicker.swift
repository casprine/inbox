import SwiftUI
import Observation
import DesignSystem
import Domain

@MainActor @Observable
public final class LabelPickerModel {
    public var query = ""
    public private(set) var labels: [String] = []
    public private(set) var isLoading = true
    public private(set) var failed = false
    private let provider: any LabelsProviding
    public init(provider: any LabelsProviding) { self.provider = provider }

    public var filtered: [String] {
        let q = query.trimmingCharacters(in: .whitespaces)
        return q.isEmpty ? labels : labels.filter { $0.localizedCaseInsensitiveContains(q) }
    }
    /// Offer "Create “x”" when the search text isn't an existing label.
    public var creatable: String? {
        let q = query.trimmingCharacters(in: .whitespaces)
        return q.isEmpty || labels.contains(where: { $0.caseInsensitiveCompare(q) == .orderedSame }) ? nil : q
    }
    public func load() async {
        do { labels = try await provider.labels(); failed = false } catch { failed = true }
        isLoading = false
    }
    public func create(_ name: String) async -> String? {
        guard let n = try? await provider.create(name) else { failed = true; return nil }
        if !labels.contains(n) { labels.append(n) }
        return n
    }
}

/// Gmail label picker sheet: search, pick, or create a new label. `selection` empty = no label.
public struct LabelPickerSheet: View {
    @Environment(\.palette) private var p
    @Environment(\.dismiss) private var dismiss
    @State private var model: LabelPickerModel
    @Binding var selection: String

    public init(provider: any LabelsProviding, selection: Binding<String>) {
        _model = State(initialValue: LabelPickerModel(provider: provider)); _selection = selection
    }

    public var body: some View {
        VStack(spacing: 0) {
            SheetHeader("Gmail label", trailingTitle: "Done") { dismiss() }
            HStack(spacing: 8) {
                Image(systemName: "magnifyingglass").foregroundStyle(p.secondary)
                TextField("Search or create a label", text: $model.query)
                    .font(.inboxBody(17)).textInputAutocapitalization(.never).autocorrectionDisabled()
            }
            .padding(.horizontal, 12).frame(minHeight: 44)
            .background(RoundedRectangle(cornerRadius: 12, style: .continuous).fill(p.grouped))
            .padding(16)
            ScrollView {
                VStack(spacing: 0) {
                    if model.failed { Banner(.warning, title: "Couldn't load labels", message: "Check Gmail in Settings.").padding(.horizontal, 16) }
                    if model.query.isEmpty { pick("No label", value: "") }
                    if let new = model.creatable {
                        Button { Task { if let n = await model.create(new) { selection = n; dismiss() } } } label: {
                            row("Create “\(new)”", selected: false, tint: p.accentText)
                        }.buttonStyle(.plain)
                    }
                    ForEach(model.filtered, id: \.self) { pick($0, value: $0) }
                    if !model.isLoading && model.filtered.isEmpty && model.creatable == nil {
                        Text("No labels").font(.inboxBody(16)).foregroundStyle(p.secondary).padding(24)
                    }
                }
            }
        }
        .background(p.ground)
        .task { await model.load() }
    }

    private func pick(_ title: String, value: String) -> some View {
        Button { selection = value; dismiss() } label: { row(title, selected: selection == value) }.buttonStyle(.plain)
    }

    private func row(_ title: String, selected: Bool, tint: Color? = nil) -> some View {
        HStack {
            Text(title).font(.inboxBody(17)).foregroundStyle(tint ?? p.text)
            Spacer()
            if selected { Image(systemName: "checkmark").foregroundStyle(p.accent) }
        }
        .padding(.horizontal, 16).frame(minHeight: 48).contentShape(Rectangle())
        .overlay(alignment: .bottom) { Rectangle().fill(p.hairline).frame(height: 0.5).padding(.leading, 16) }
    }
}
