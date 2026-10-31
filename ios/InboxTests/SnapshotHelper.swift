import SwiftUI
import UIKit
import Testing

/// Minimal snapshot testing (no third-party package): hosts a SwiftUI view in a 390×844 window, waits for async loads,
/// renders it and compares with a PNG baseline in `InboxTests/Snapshots/`. Baselines are recorded on iPhone 17 Pro / iOS 26.5.
///
/// Record or refresh with `make ios-record-snapshots`; a missing baseline fails the test.
@MainActor
enum Snapshot {
    static let size = CGSize(width: 390, height: 844)
    /// Share of pixels (any channel off by more than `channelTolerance`) allowed to differ.
    static let allowedDiff = 0.003
    static let channelTolerance = 10

    static var recording: Bool { ProcessInfo.processInfo.environment["RECORD_SNAPSHOTS"] == "1" }

    static func baselineURL(_ name: String, _ scheme: ColorScheme, file: String = #filePath) -> URL {
        URL(fileURLWithPath: file).deletingLastPathComponent().appendingPathComponent("Snapshots/\(name)-\(scheme == .dark ? "dark" : "light").png")
    }

    /// A render where (almost) every sampled pixel is the same colour: the window wasn't drawn (inactive simulator display).
    static func isBlank(_ image: UIImage) -> Bool {
        guard let px = pixels(image), px.count >= 4 else { return true }
        var seen = Set<UInt32>()
        var i = 0
        let step = max((px.count / 4) / 4_000, 1) * 4
        while i + 3 < px.count && seen.count < 12 {
            seen.insert(UInt32(px[i]) << 16 | UInt32(px[i + 1]) << 8 | UInt32(px[i + 2])); i += step
        }
        return seen.count < 6
    }

    /// Renders `view` after `settle`; retries (up to 4 times) when the result is blank.
    static func render<V: View>(_ view: V, scheme: ColorScheme, settle: Duration = .milliseconds(1_200), height: CGFloat = 844) async -> UIImage {
        var image = await renderOnce(view, scheme: scheme, settle: settle, height: height)
        var attempts = 1
        while isBlank(image) && attempts < 4 {
            attempts += 1
            image = await renderOnce(view, scheme: scheme, settle: settle * 2, height: height)
        }
        return image
    }

    private static func renderOnce<V: View>(_ view: V, scheme: ColorScheme, settle: Duration, height: CGFloat) async -> UIImage {
        let size = CGSize(width: Self.size.width, height: height)
        let host = UIHostingController(rootView: AnyView(view.frame(width: size.width, height: size.height).preferredColorScheme(scheme)))
        host.overrideUserInterfaceStyle = scheme == .dark ? .dark : .light
        let scene = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first
        let window = scene.map { UIWindow(windowScene: $0) } ?? UIWindow()
        window.frame = CGRect(origin: .zero, size: size)
        window.rootViewController = host
        window.makeKeyAndVisible()
        host.view.frame = window.bounds
        host.view.layoutIfNeeded()
        try? await Task.sleep(for: settle)
        host.view.layoutIfNeeded()
        let renderer = UIGraphicsImageRenderer(size: size, format: { let f = UIGraphicsImageRendererFormat(); f.scale = 1; f.opaque = true; return f }())
        let image = renderer.image { _ in window.drawHierarchy(in: window.bounds, afterScreenUpdates: true) }
        window.isHidden = true
        return image
    }

    /// Compares with the baseline (or records it). Returns a human-readable failure, nil when it matches.
    static func compare(_ image: UIImage, name: String, scheme: ColorScheme, file: String = #filePath) -> String? {
        let url = baselineURL(name, scheme, file: file)
        if isBlank(image) { return "\(name) (\(scheme)): rendered blank after retries (is the simulator display active?)." }
        if recording {
            try? FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
            try? image.pngData()?.write(to: url)
            return nil
        }
        guard let data = try? Data(contentsOf: url), let base = UIImage(data: data) else {
            return "No baseline for \(name) (\(scheme)). Run `make ios-record-snapshots`."
        }
        guard let a = pixels(image), let b = pixels(base), a.count == b.count else { return "\(name): size differs from the baseline." }
        var bad = 0
        var i = 0
        while i < a.count {
            if abs(Int(a[i]) - Int(b[i])) > channelTolerance || abs(Int(a[i + 1]) - Int(b[i + 1])) > channelTolerance || abs(Int(a[i + 2]) - Int(b[i + 2])) > channelTolerance { bad += 1 }
            i += 4
        }
        let ratio = Double(bad) / Double(a.count / 4)
        if ratio > allowedDiff {
            // Leave the new rendering next to the test output so a reviewer can look at it.
            let out = FileManager.default.temporaryDirectory.appendingPathComponent("snapshot-FAILED-\(name)-\(scheme == .dark ? "dark" : "light").png")
            try? image.pngData()?.write(to: out)
            return "\(name) (\(scheme)): \(String(format: "%.2f", ratio * 100))% of pixels differ. New rendering: \(out.path)"
        }
        return nil
    }

    private static func pixels(_ image: UIImage) -> [UInt8]? {
        guard let cg = image.cgImage else { return nil }
        let w = cg.width, h = cg.height
        var buf = [UInt8](repeating: 0, count: w * h * 4)
        guard let ctx = CGContext(data: &buf, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4, space: CGColorSpaceCreateDeviceRGB(),
                                  bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return nil }
        ctx.draw(cg, in: CGRect(x: 0, y: 0, width: w, height: h))
        return buf
    }

    /// Light and dark in one call; records a test issue per mismatch.
    static func check<V: View>(_ name: String, settle: Duration = .milliseconds(1_200), typeSize: DynamicTypeSize? = nil, height: CGFloat = 844, file: String = #filePath, line: Int = #line,
                               _ make: () -> V) async {
        for scheme in [ColorScheme.light, .dark] {
            let image = await render(make().environment(\.dynamicTypeSize, typeSize ?? .large), scheme: scheme, settle: settle, height: height)
            if let failure = compare(image, name: name, scheme: scheme, file: file) {
                Issue.record(Comment(rawValue: failure), sourceLocation: SourceLocation(fileID: "InboxTests/Snapshot", filePath: file, line: line, column: 1))
            }
        }
    }
}
