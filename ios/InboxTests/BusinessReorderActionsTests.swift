import SwiftUI
import UIKit
import Testing
import API
import Persistence
import Domain
import DesignSystem
@testable import FeatureBusinesses

/// BZ02 VoiceOver path (D3): the real Edit list is hosted in a window and its accessibility tree is read the way VoiceOver reads it.
/// Every row must offer "Move up" / "Move down" (not on the first / last row), and performing one must reorder the stored list.
/// XCUITest cannot invoke custom accessibility actions, so this is the closest automated check; spoken output is not covered.
@MainActor @Suite(.serialized) struct BusinessReorderActionsTests {
    /// All accessibility elements below `root`, in tree order (containers expose children through the UIAccessibilityContainer API).
    private func elements(_ root: NSObject, depth: Int = 0) -> [NSObject] {
        guard depth < 40 else { return [] }
        var out: [NSObject] = []
        let n = root.accessibilityElementCount()
        if n != NSNotFound, n > 0 {
            for i in 0..<n { if let e = root.accessibilityElement(at: i) as? NSObject { out.append(e); out += elements(e, depth: depth + 1) } }
        } else if let list = root.accessibilityElements {
            for case let e as NSObject in list { out.append(e); out += elements(e, depth: depth + 1) }
        }
        if let v = root as? UIView { for s in v.subviews { out += elements(s, depth: depth + 1) } }
        return out
    }

    /// SwiftUI builds its accessibility tree only while accessibility is on (UI tests and VoiceOver switch it on; a unit-test host does not).
    private func enableAccessibility() {
        typealias SetEnabled = @convention(c) (Bool) -> Void
        if let lib = dlopen("/usr/lib/libAccessibility.dylib", RTLD_NOW), let sym = dlsym(lib, "_AXSSetAutomationEnabled") {
            unsafeBitCast(sym, to: SetEnabled.self)(true)
        }
    }

    private func host(_ model: BusinessesSettingsModel, env: AppEnvironment) async -> (UIWindow, UIHostingController<AnyView>) {
        let vc = UIHostingController(rootView: AnyView(NavigationStack { BusinessEditList(model: model) }.inboxTheme(.system)))
        let scene = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first
        let window = scene.map { UIWindow(windowScene: $0) } ?? UIWindow()
        window.frame = CGRect(x: 0, y: 0, width: 390, height: 844)
        window.rootViewController = vc
        window.makeKeyAndVisible()
        try? await Task.sleep(for: .milliseconds(1_000))
        return (window, vc)
    }

    private func actions(_ list: [NSObject], containing name: String) -> [UIAccessibilityCustomAction] {
        let row = list.first { ($0.accessibilityLabel ?? "").contains(name) && !($0.accessibilityCustomActions ?? []).isEmpty }
        return row?.accessibilityCustomActions ?? []
    }

    @Test func rowsOfferMoveUpAndDownAndPerformingOneReorders() async throws {
        let env = try AppEnvironment.preview()
        try await MockData.seed(env.database)
        let model = BusinessesSettingsModel(env: env)
        model.observe_once_for_tests(try await env.businesses.all())   // Acme, Northgate, Lumen
        enableAccessibility()
        let (window, vc) = await host(model, env: env)
        defer { window.isHidden = true }

        let tree = elements(vc.view)
        // The merged row element also carries its switch's own action ("Show … in Mail"); only the move actions matter here.
        func moves(_ name: String) -> [String] { actions(tree, containing: name).map(\.name).filter { $0.hasPrefix("Move") } }
        let first = moves("Acme Studio"), middle = moves("Northgate Ltd"), last = moves("Lumen Hosting")
        #expect(first == ["Move down"], "first row: only Move down (got \(first))")
        #expect(Set(middle) == ["Move up", "Move down"], "middle row offers both (got \(middle))")
        #expect(last == ["Move up"], "last row: only Move up (got \(last))")

        // Perform "Move up" on Northgate: it goes above Acme, and the order is stored.
        let up = try #require(actions(tree, containing: "Northgate Ltd").first { $0.name == "Move up" })
        _ = up.actionHandler?(up)
        try await Task.sleep(for: .milliseconds(800))
        #expect(try await env.businesses.all().map(\.id) == [MockData.northgate, MockData.acme, MockData.lumen])
    }
}
