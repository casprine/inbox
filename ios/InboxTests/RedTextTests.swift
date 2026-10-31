import Foundation
import Testing

/// H-1 (D-013): red text is always `accentText`. `destructive` is only a button fill; raw `accent` is for icons, dots and rings.
/// Scans the Swift sources, so a new `Text(...).foregroundStyle(p.destructive)` or `... p.accent)` fails the build.
@Suite struct RedTextTests {
    private static var sources: URL {
        URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent().appendingPathComponent("InboxKit/Sources")
    }
    private static func swiftFiles() -> [URL] {
        let e = FileManager.default.enumerator(at: sources, includingPropertiesForKeys: nil)
        return (e?.allObjects as? [URL] ?? []).filter { $0.pathExtension == "swift" }
    }

    @Test func noRedTextUsesWrongTokens() throws {
        var offenders: [String] = []
        let files = Self.swiftFiles()
        #expect(files.count > 20)
        for url in files {
            let name = url.lastPathComponent
            if name == "Palette.swift" || name == "Buttons.swift" { continue }   // Buttons.swift: destructive as the button fill
            let lines = try String(contentsOf: url, encoding: .utf8).components(separatedBy: "\n")
            for (i, line) in lines.enumerated() {
                let code = line.components(separatedBy: "//").first ?? line
                let usesForeground = code.contains("foregroundStyle(") || code.contains("foregroundColor(")
                let isIconOrShape = code.contains("Image(") || code.contains("Circle()") || code.contains("Capsule()")
                    || code.contains(".buttonStyle(.plain).foregroundStyle(p.accent)")   // toolbar icon buttons (magnifier, plus)
                    || code.contains("}.font(.system(size:")                                // stepper icon buttons
                if code.contains("p.destructive") && usesForeground { offenders.append("\(name):\(i + 1) destructive as foreground") }
                if code.contains("p.destructive") && code.contains("color:") { offenders.append("\(name):\(i + 1) destructive as text colour") }
                if usesForeground, !isIconOrShape, code.range(of: #"foreground(Style|Color)\([^)]*\bp\.accent\b(?!Text|Fill)"#, options: .regularExpression) != nil,
                   code.contains("Text(") || code.contains(".inboxBody(") {
                    offenders.append("\(name):\(i + 1) raw accent on text")
                }
                if code.contains("hasPrefix(\"pass\")") && code.contains("p.accent)") { offenders.append("\(name):\(i + 1) raw accent on text") }
            }
        }
        #expect(offenders.isEmpty, "Red text must use accentText: \(offenders)")
    }
}
