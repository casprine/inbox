import Foundation
import Observation
import Persistence

/// Appearance choice (ST03), persisted in `pref.appearance.theme`. DesignSystem maps the raw value to `ThemeMode`.
@Observable
public final class ThemePreference: @unchecked Sendable {
    public static let key = "appearance.theme"
    public static let imagesKey = "reading.load_images"
    public static let linesKey = "reading.preview_lines"
    public private(set) var raw: String = "system"
    public private(set) var previewLines: Int = 2
    /// Set by the app so a change is also sent to `PUT /v1/prefs`.
    @MainActor public var onPreviewLinesChanged: ((Int) -> Void)?
    public init() {}

    @MainActor public func load(_ db: AppDatabase) async {
        if let v = (try? await db.pref(Self.key)) ?? nil, ["system", "light", "dark"].contains(v) { raw = v }
        if let v = (try? await db.pref(Self.linesKey)) ?? nil, let n = Int(v), (1...4).contains(n) { previewLines = n }
    }
    @MainActor public func set(_ value: String, _ db: AppDatabase) async {
        raw = value; try? await db.setPref(Self.key, value)
    }
    @MainActor public func setPreviewLines(_ n: Int, _ db: AppDatabase) async {
        previewLines = n; try? await db.setPref(Self.linesKey, String(n))
        onPreviewLinesChanged?(n)
    }
}
