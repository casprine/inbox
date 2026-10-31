import SwiftUI
import DesignSystem

/// The licence text that ships with the bundled font (`Inbox/Fonts/OFL.txt`, SIL Open Font License 1.1).
public enum FontLicence {
    public static let fallback = "The licence text could not be loaded. The Open Runde typeface is used under the SIL Open Font License, Version 1.1 (https://openfontlicense.org)."
    /// Full text from the app bundle; `fallback` when the file is missing.
    public static func text(bundle: Bundle = .main) -> String {
        guard let url = bundle.url(forResource: "OFL", withExtension: "txt"), let s = try? String(contentsOf: url, encoding: .utf8), !s.isEmpty else { return fallback }
        return s
    }
}

extension FontLicence {
    public struct Paragraph: Hashable, Sendable { public let text: String; public let isHeading: Bool }

    /// The file is hard-wrapped at about 75 columns, which breaks badly on a phone. Same words, re-flowed: lines of one paragraph are
    /// joined, numbered items and all-caps headings start their own paragraph, and the dashed rules are dropped.
    public static func paragraphs(_ raw: String) -> [Paragraph] {
        var out: [Paragraph] = []
        var current: [String] = []
        func flush() { if !current.isEmpty { out.append(Paragraph(text: current.joined(separator: " "), isHeading: false)); current = [] } }
        for line in raw.replacingOccurrences(of: "\r\n", with: "\n").split(separator: "\n", omittingEmptySubsequences: false) {
            let t = line.trimmingCharacters(in: .whitespaces)
            if t.isEmpty || t.allSatisfy({ $0 == "-" }) { flush(); continue }
            let letters = t.filter(\.isLetter)
            if letters.count >= 4, letters == letters.uppercased(), !t.contains(".") { flush(); out.append(Paragraph(text: t, isHeading: true)); continue }
            if let first = t.first, first.isNumber, t.dropFirst().first == ")" { flush() }
            current.append(t)
        }
        flush()
        return out
    }
}

/// Settings → About → Open Runde: the full OFL text, selectable and scrollable (C-21).
public struct LicenceScreen: View {
    @Environment(\.palette) private var p
    let text: String
    public init(text: String = FontLicence.text()) { self.text = text }

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 12) {
                Text("Open Runde").font(.inboxHeadingBold(22, relativeTo: .title2)).foregroundStyle(p.text)
                    .accessibilityAddTraits(.isHeader)
                Text("The typeface used throughout Inbox, under the SIL Open Font License, Version 1.1.")
                    .font(.inboxBody(15, relativeTo: .subheadline)).foregroundStyle(p.secondary)
                VStack(alignment: .leading, spacing: 10) {
                    ForEach(Array(FontLicence.paragraphs(text).enumerated()), id: \.offset) { _, para in
                        Text(para.text)
                            .font(.inboxBody(14, medium: para.isHeading, relativeTo: .footnote)).foregroundStyle(p.text).lineSpacing(3)
                            .accessibilityAddTraits(para.isHeading ? .isHeader : [])
                    }
                }
                .textSelection(.enabled)
                    .padding(16).frame(maxWidth: .infinity, alignment: .leading)
                    .background(RoundedRectangle(cornerRadius: 12, style: .continuous).fill(p.card))
                    .accessibilityElement(children: .contain).accessibilityIdentifier("licence-text")
            }
            .fixedSize(horizontal: false, vertical: true)
            .padding(16)
        }
        .background(p.ground)
        .inboxPushed(title: "Open Runde", back: "About")
    }
}
