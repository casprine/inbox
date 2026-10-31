import SwiftUI

public extension Color {
    /// sRGB colour from a 0xRRGGBB literal.
    static func hex(_ value: UInt32) -> Color {
        Color(.sRGB,
              red: Double((value >> 16) & 0xFF) / 255,
              green: Double((value >> 8) & 0xFF) / 255,
              blue: Double(value & 0xFF) / 255,
              opacity: 1)
    }
}

/// Design tokens from the Foundations board (plan §8.5).
public struct Palette: Sendable, Equatable {
    public struct Hex: Sendable, Equatable {
        public let ground, card, grouped, hairline, text, secondary, tertiary, accent, onAccent: UInt32
        public var accentText: UInt32 = 0, accentFill: UInt32 = 0, textTertiary: UInt32 = 0, hairlineStrong: UInt32 = 0
        public var field: UInt32 = 0, highlight: UInt32 = 0, knob: UInt32 = 0, destructive: UInt32 = 0, warning: UInt32 = 0
    }
    public let hexes: Hex
    public var ground: Color { .hex(hexes.ground) }
    public var card: Color { .hex(hexes.card) }
    public var grouped: Color { .hex(hexes.grouped) }
    public var hairline: Color { .hex(hexes.hairline) }
    public var text: Color { .hex(hexes.text) }
    public var secondary: Color { .hex(hexes.secondary) }
    public var tertiary: Color { .hex(hexes.tertiary) }
    public var accent: Color { .hex(hexes.accent) }
    public var onAccent: Color { .hex(hexes.onAccent) }
    /// Token spec v1.1 (docs/design-specs/tokens.md). `accentText` for any accent-coloured text; `accent` for dots, bars, icons.
    public var accentText: Color { .hex(hexes.accentText) }
    public var accentFill: Color { .hex(hexes.accentFill) }
    public var textTertiary: Color { .hex(hexes.textTertiary) }
    public var hairlineStrong: Color { .hex(hexes.hairlineStrong) }
    public var field: Color { .hex(hexes.field) }
    public var highlight: Color { .hex(hexes.highlight) }
    public var knob: Color { .hex(hexes.knob) }
    public var destructive: Color { .hex(hexes.destructive) }
    public var warning: Color { .hex(hexes.warning) }

    public init(hexes: Hex) { self.hexes = hexes }

    public static let light = Palette(hexes: .init(
        ground: 0xF1E9D2, card: 0xF7F1E2, grouped: 0xE9DEC0, hairline: 0xDCCFBC,
        text: 0x461634, secondary: 0x7A5268, tertiary: 0xA68C97, accent: 0xF83D34, onAccent: 0xF1E9D2,
        accentText: 0xBD251D, accentFill: 0xC8281F, textTertiary: 0x7E576C, hairlineStrong: 0xCDBDA5,
        field: 0xE5D9BC, highlight: 0xFCE2DA, knob: 0xFFFDF7, destructive: 0xD92D20, warning: 0xB45309))
    public static let dark = Palette(hexes: .init(
        ground: 0x1A0F16, card: 0x24151F, grouped: 0x130A10, hairline: 0x36222F,
        text: 0xF3EBD8, secondary: 0xC2AFB8, tertiary: 0x7E6B76, accent: 0xFF5B45, onAccent: 0x1A0F16,
        accentText: 0xFF5B45, accentFill: 0xFF5B45, textTertiary: 0x907D87, hairlineStrong: 0x4A3342,
        field: 0x2C1B26, highlight: 0x4A2220, knob: 0xF3EBD8, destructive: 0xF0584A, warning: 0xE0A03A))

    public static func resolved(for scheme: ColorScheme) -> Palette {
        scheme == .dark ? .dark : .light
    }
}
