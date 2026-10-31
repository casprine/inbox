import SwiftUI

public enum ThemeMode: String, CaseIterable, Sendable, Identifiable {
    case system, light, dark
    public var id: String { rawValue }

    /// The concrete scheme given the system's current scheme.
    public func resolve(system: ColorScheme) -> ColorScheme {
        switch self {
        case .system: system
        case .light: .light
        case .dark: .dark
        }
    }
    /// Value for `preferredColorScheme` (nil follows the system).
    public var override: ColorScheme? {
        switch self {
        case .system: nil
        case .light: .light
        case .dark: .dark
        }
    }
}

private struct PaletteKey: EnvironmentKey { static let defaultValue = Palette.light }

public extension EnvironmentValues {
    var palette: Palette {
        get { self[PaletteKey.self] }
        set { self[PaletteKey.self] = newValue }
    }
}

private struct ThemeRoot: ViewModifier {
    let mode: ThemeMode
    @Environment(\.colorScheme) private var system
    @Environment(\.dynamicTypeSize) private var typeSize
    func body(content: Content) -> some View {
        let scheme = mode.resolve(system: system)
        InboxType.size = typeSize
        return content
            .environment(\.palette, .resolved(for: scheme))
            .preferredColorScheme(mode.override)
            .id(typeSize)
    }
}

public extension View {
    /// Apply once at the root; injects the palette and honours light/dark/system.
    func inboxTheme(_ mode: ThemeMode) -> some View { modifier(ThemeRoot(mode: mode)) }
}
