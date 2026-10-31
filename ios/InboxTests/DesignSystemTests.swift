import SwiftUI
import Testing
@testable import DesignSystem

@Suite struct PaletteTests {
    @Test func lightHex() {
        let h = Palette.light.hexes
        #expect(h.ground == 0xF1E9D2); #expect(h.card == 0xF7F1E2); #expect(h.grouped == 0xE9DEC0)
        #expect(h.hairline == 0xDCCFBC); #expect(h.text == 0x461634); #expect(h.secondary == 0x7A5268)
        #expect(h.tertiary == 0xA68C97); #expect(h.accent == 0xF83D34); #expect(h.onAccent == 0xF1E9D2)
    }
    @Test func darkHex() {
        let h = Palette.dark.hexes
        #expect(h.ground == 0x1A0F16); #expect(h.card == 0x24151F); #expect(h.grouped == 0x130A10)
        #expect(h.hairline == 0x36222F); #expect(h.text == 0xF3EBD8); #expect(h.secondary == 0xC2AFB8)
        #expect(h.tertiary == 0x7E6B76); #expect(h.accent == 0xFF5B45); #expect(h.onAccent == 0x1A0F16)
    }
    @Test func hexColorComponents() {
        let c = UIColor(Color.hex(0xF83D34))
        var r: CGFloat = 0, g: CGFloat = 0, b: CGFloat = 0, a: CGFloat = 0
        c.getRed(&r, green: &g, blue: &b, alpha: &a)
        #expect(abs(r - 248.0/255) < 0.01); #expect(abs(g - 61.0/255) < 0.01); #expect(abs(b - 52.0/255) < 0.01)
    }
}

@Suite struct ThemeTests {
    @Test func resolution() {
        #expect(ThemeMode.system.resolve(system: .dark) == .dark)
        #expect(ThemeMode.system.resolve(system: .light) == .light)
        #expect(ThemeMode.light.resolve(system: .dark) == .light)
        #expect(ThemeMode.dark.resolve(system: .light) == .dark)
    }
    @Test func overrideAndPalette() {
        #expect(ThemeMode.system.override == nil)
        #expect(ThemeMode.dark.override == .dark)
        #expect(Palette.resolved(for: .dark) == .dark)
        #expect(Palette.resolved(for: .light) == .light)
    }
}

@Suite(.serialized) struct AccessibilityTests {
    @Test func scaledFontsGrowWithDynamicTypeAndRestore() {
        let before = InboxType.size
        defer { InboxType.size = before }
        InboxType.size = .large
        let normal = inboxScaled(16, .body)
        InboxType.size = .accessibility3
        let big = inboxScaled(16, .body)
        InboxType.size = .xSmall
        let small = inboxScaled(16, .body)
        #expect(big > normal * 1.5 && small < normal)
        #expect(InboxType.category(.accessibility3) == .accessibilityExtraLarge && InboxType.category(.large) == .large)
    }

    @Test func accessibilitySizesAreDetected() {
        #expect(DynamicTypeSize.accessibility1.isAccessibilitySize && !DynamicTypeSize.xxxLarge.isAccessibilitySize)
    }
}
